// Package project owns projects and their membership.
//
// Every write here goes through policy.Check* before it touches the database.
// The handler above supplies the set of fields being written and never names
// one, which is what makes adding a field to the ownership table sufficient to
// enforce it.
package project

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Service is the projects application service.
type Service struct {
	db  *store.DB
	log *slog.Logger

	projects repo.Projects
	members  repo.Members
	users    repo.Users
	logs     repo.AuditLogs
}

// New builds the service.
func New(db *store.DB, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, log: log}
}

// subject resolves the caller's standing on a project.
func (s *Service) subject(ctx context.Context, q store.Querier, p policy.Principal, projectID uuid.UUID) (policy.Subject, error) {
	override, err := s.members.RoleFor(ctx, q, projectID, p.UserID)
	if err != nil {
		return policy.Subject{}, err
	}
	return policy.Subject{
		Principal:     p,
		ProjectID:     projectID,
		EffectiveRole: policy.Effective(p.GlobalRole, override),
		IsMember:      override != nil,
	}, nil
}

// ── read ─────────────────────────────────────────────────────────────────────

// List returns the projects the caller may see.
func (s *Service) List(ctx context.Context, p policy.Principal) ([]*domain.Project, error) {
	return s.projects.ListVisible(ctx, s.db.Pool, p.UserID, p.GlobalRole == policy.RoleAdmin)
}

// Get returns one project the caller may see.
//
// A non-member gets not_found rather than forbidden. Distinguishing them would
// let anyone enumerate which project keys exist, and "you may not see this
// project" and "there is no such project" are the same fact from outside.
func (s *Service) Get(ctx context.Context, id uuid.UUID, p policy.Principal) (*domain.Project, error) {
	proj, err := s.projects.ByID(ctx, s.db.Pool, id)
	if err != nil {
		return nil, err
	}
	if p.GlobalRole == policy.RoleAdmin {
		return proj, nil
	}
	override, err := s.members.RoleFor(ctx, s.db.Pool, id, p.UserID)
	if err != nil {
		return nil, err
	}
	if override == nil {
		return nil, fmt.Errorf("%w: project", domain.ErrNotFound)
	}
	return proj, nil
}

// ── write ────────────────────────────────────────────────────────────────────

// CreateInput describes a new project.
type CreateInput struct {
	Key         string
	Name        string
	Description string
	Status      string
}

// Create makes a project and adds its creator as a member.
//
// The creator's membership is part of the same operation: an admin who creates
// a project and is then not a member of it produces a project nobody can reach
// through the non-admin path.
func (s *Service) Create(ctx context.Context, in CreateInput, p policy.Principal) (*domain.Project, error) {
	subject := policy.Subject{Principal: p, EffectiveRole: p.GlobalRole, IsMember: true}
	if d := policy.CheckCreate(policy.KindProject, subject); d != nil {
		return nil, d
	}

	status := in.Status
	if status == "" {
		status = domain.ProjectActive
	}
	if err := domain.ValidateProject(in.Key, in.Name, status); err != nil {
		return nil, err
	}

	now := time.Now()
	proj := &domain.Project{
		ID:          uuid.Must(uuid.NewV7()),
		Key:         in.Key,
		Name:        in.Name,
		Description: in.Description,
		Status:      status,
		Settings:    map[string]any{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.projects.Create(ctx, tx, proj); err != nil {
			return err
		}
		if err := s.members.Put(ctx, tx, domain.ProjectMember{
			ProjectID: proj.ID, UserID: p.UserID, Role: policy.RoleManager,
		}); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &proj.ID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionProjectCreated,
			SubjectID:  &proj.ID,
			After:      publicProject(proj),
		})
	})
	if err != nil {
		return nil, err
	}
	return proj, nil
}

// UpdateInput is a patch. Fields is the set of wire names the request actually
// set, derived by policy.Fields from the request body — the service never
// inspects which particular field it is.
type UpdateInput struct {
	Fields []string

	Name        *string
	Description *string
	Status      *string
	Settings    *map[string]any
	GitEnabled  *bool
}

// Update patches a project after checking every field against the ownership
// table.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput, p policy.Principal) (*domain.Project, error) {
	var updated *domain.Project

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		proj, err := s.projects.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		subject, err := s.subject(ctx, tx, p, id)
		if err != nil {
			return err
		}
		// Before any write, for the complete set of fields. Checking as each
		// field is applied would leave a half-applied update behind the first
		// denial.
		if d := policy.CheckWrite(policy.KindProject, in.Fields, subject); d != nil {
			return d
		}

		before := publicProject(proj)

		if in.Name != nil {
			proj.Name = *in.Name
		}
		if in.Description != nil {
			proj.Description = *in.Description
		}
		if in.Status != nil {
			proj.Status = *in.Status
		}
		if in.Settings != nil {
			proj.Settings = *in.Settings
		}
		if in.GitEnabled != nil {
			proj.GitEnabled = *in.GitEnabled
		}

		if err := domain.ValidateProject(proj.Key, proj.Name, proj.Status); err != nil {
			return err
		}
		if err := s.projects.Update(ctx, tx, proj); err != nil {
			return err
		}

		updated = proj
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &proj.ID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionProjectUpdated,
			SubjectID:  &proj.ID,
			Before:     before,
			After:      publicProject(proj),
		})
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// Delete soft-deletes a project.
func (s *Service) Delete(ctx context.Context, id uuid.UUID, p policy.Principal) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		proj, err := s.projects.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		subject, err := s.subject(ctx, tx, p, id)
		if err != nil {
			return err
		}
		if d := policy.CheckDelete(policy.KindProject, subject); d != nil {
			return d
		}
		if err := s.projects.Delete(ctx, tx, id); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &id,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionProjectDeleted,
			SubjectID:  &id,
			Before:     publicProject(proj),
		})
	})
}

// ── membership ───────────────────────────────────────────────────────────────

// Members returns a project's membership with each member's effective role.
func (s *Service) Members(ctx context.Context, projectID uuid.UUID, p policy.Principal) ([]repo.MemberRow, error) {
	if _, err := s.Get(ctx, projectID, p); err != nil {
		return nil, err
	}
	return s.members.List(ctx, s.db.Pool, projectID)
}

// PutMember adds a member or changes their project role. Admin only (§7.1).
func (s *Service) PutMember(ctx context.Context, projectID, userID uuid.UUID, role policy.Role, p policy.Principal) error {
	if p.GlobalRole != policy.RoleAdmin {
		return &policy.Denial{
			Kind:          policy.DenialForbidden,
			Object:        policy.KindProject,
			OwningRole:    policy.OwnerAdmin,
			EffectiveRole: p.GlobalRole,
			Route:         "ask an admin",
			Message: fmt.Sprintf("To change who is on a project, ask an admin. Membership is admin-owned, and your role is %s.",
				p.GlobalRole),
		}
	}
	if !role.Valid() {
		return fmt.Errorf("%w: %q is not a role; the roles are admin, manager, developer", domain.ErrInvalid, role)
	}

	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.projects.ByID(ctx, tx, projectID); err != nil {
			return err
		}
		if _, err := s.users.ByID(ctx, tx, userID); err != nil {
			return err
		}

		existing, err := s.members.RoleFor(ctx, tx, projectID, userID)
		if err != nil {
			return err
		}
		if existing != nil && *existing == role {
			return nil // no change, no audit row
		}

		if err := s.members.Put(ctx, tx, domain.ProjectMember{
			ProjectID: projectID, UserID: userID, Role: role,
		}); err != nil {
			return err
		}

		action := audit.ActionMemberAdded
		var before any
		if existing != nil {
			// A role change is its own transition, not a generic update.
			action = audit.ActionMemberRoleChanged
			before = map[string]any{"user_id": userID, "role": string(*existing)}
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &projectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     action,
			SubjectID:  &userID,
			Before:     before,
			After:      map[string]any{"user_id": userID, "role": string(role)},
		})
	})
}

// RemoveMember drops a membership. Admin only.
func (s *Service) RemoveMember(ctx context.Context, projectID, userID uuid.UUID, p policy.Principal) error {
	if p.GlobalRole != policy.RoleAdmin {
		return &policy.Denial{
			Kind:          policy.DenialForbidden,
			Object:        policy.KindProject,
			OwningRole:    policy.OwnerAdmin,
			EffectiveRole: p.GlobalRole,
			Route:         "ask an admin",
			Message: fmt.Sprintf("To change who is on a project, ask an admin. Membership is admin-owned, and your role is %s.",
				p.GlobalRole),
		}
	}

	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		existing, err := s.members.RoleFor(ctx, tx, projectID, userID)
		if err != nil {
			return err
		}
		if existing == nil {
			return fmt.Errorf("%w: project member", domain.ErrNotFound)
		}
		if err := s.members.Remove(ctx, tx, projectID, userID); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &projectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionMemberRemoved,
			SubjectID:  &userID,
			Before:     map[string]any{"user_id": userID, "role": string(*existing)},
		})
	})
}

func publicProject(p *domain.Project) map[string]any {
	return map[string]any{
		"id":          p.ID,
		"key":         p.Key,
		"name":        p.Name,
		"description": p.Description,
		"status":      p.Status,
		"settings":    p.Settings,
		"git_enabled": p.GitEnabled,
	}
}
