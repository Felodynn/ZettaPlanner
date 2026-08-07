package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// User management is admin-only (CONTRACT §4.1). The role check lives at the
// top of each method rather than in the handler, so the CLI bootstrap path and
// the HTTP path cannot disagree about who may do this.

// CreateUserInput describes a new account.
type CreateUserInput struct {
	Email       string
	DisplayName string
	Password    string
	Role        policy.Role
	IsActive    *bool
}

// CreateUser creates an account and its actor row.
//
// One logical operation: the user, the actor, and one audit entry, in one
// transaction. Not two entries — an actor without its user is not a thing that
// happened, and the v0.1 export reads better for the distinction.
func (s *Service) CreateUser(ctx context.Context, in CreateUserInput, by policy.Principal) (*domain.User, error) {
	if by.GlobalRole != policy.RoleAdmin {
		return nil, &policy.Denial{
			Kind:          policy.DenialForbidden,
			OwningRole:    policy.OwnerAdmin,
			EffectiveRole: by.GlobalRole,
			Route:         "ask an admin",
			Message:       "To create an account, ask an admin. User management is admin-owned, and your role is " + string(by.GlobalRole) + ".",
		}
	}
	return s.createUser(ctx, in, &by)
}

// Bootstrap creates the first account, with no principal to authorise it.
//
// Only legitimate while the deployment has no users at all: without it there is
// no admin, and without an admin there is no way to make one. It refuses the
// moment a user exists, so it cannot become a back door.
func (s *Service) Bootstrap(ctx context.Context, in CreateUserInput) (*domain.User, error) {
	existing, err := s.users.List(ctx, s.db.Pool)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		return nil, fmt.Errorf("%w: this deployment already has %d accounts; create further ones as an admin",
			domain.ErrConflict, len(existing))
	}
	if in.Role != policy.RoleAdmin {
		return nil, fmt.Errorf("%w: the first account must be an admin, otherwise nobody can create the second",
			domain.ErrInvalid)
	}
	return s.createUser(ctx, in, nil)
}

// createUser is the shared path. by is nil for the bootstrap, where the write
// is attributed to the system actor.
func (s *Service) createUser(ctx context.Context, in CreateUserInput, by *policy.Principal) (*domain.User, error) {
	email := domain.NormaliseEmail(in.Email)
	if err := domain.ValidateUser(email, in.DisplayName, in.Role); err != nil {
		return nil, err
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalid, err)
	}

	active := true
	if in.IsActive != nil {
		active = *in.IsActive
	}

	now := time.Now()
	user := &domain.User{
		ID:           uuid.Must(uuid.NewV7()),
		Email:        email,
		DisplayName:  in.DisplayName,
		PasswordHash: hash,
		Role:         in.Role,
		IsActive:     active,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.users.Create(ctx, tx, user); err != nil {
			return err
		}

		// The actor exists from the moment the user does. An audit row can then
		// never reference a principal that has not been created yet.
		actor := &domain.Actor{
			ID:        uuid.Must(uuid.NewV7()),
			Kind:      domain.ActorHuman,
			UserID:    &user.ID,
			CreatedAt: now,
		}
		if err := s.actors.Create(ctx, tx, actor); err != nil {
			return err
		}

		writer := actor.ID
		var onBehalf *uuid.UUID
		if by != nil {
			writer = by.WritingActorID()
			onBehalf = by.OnBehalfOf()
		} else {
			sys, err := s.actors.System(ctx, tx)
			if err != nil {
				return err
			}
			writer = sys.ID
		}

		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    writer,
			OnBehalfOf: onBehalf,
			Action:     audit.ActionUserCreated,
			SubjectID:  &user.ID,
			After:      publicUser(user, actor.ID),
		})
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

// ListUsers returns every live account. Admin only.
func (s *Service) ListUsers(ctx context.Context, by policy.Principal) ([]*domain.User, error) {
	if by.GlobalRole != policy.RoleAdmin {
		return nil, adminOnly(by, "list accounts")
	}
	return s.users.List(ctx, s.db.Pool)
}

// UpdateUserInput is a patch. Nil means "leave alone".
type UpdateUserInput struct {
	DisplayName *string
	Role        *policy.Role
	IsActive    *bool
	Password    *string
}

// UpdateUser patches an account.
//
// A role change is recorded as `user.role_changed`, not `user.updated`, even
// when other fields moved in the same request: the transition is the fact worth
// finding later, and before/after still carry everything else (§6.5
// constraint 2).
func (s *Service) UpdateUser(ctx context.Context, id uuid.UUID, in UpdateUserInput, by policy.Principal) (*domain.User, error) {
	if by.GlobalRole != policy.RoleAdmin {
		return nil, adminOnly(by, "change an account")
	}

	var updated *domain.User
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		user, err := s.users.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		actor, err := s.actors.ForUser(ctx, tx, user.ID)
		if err != nil {
			return err
		}
		before := publicUser(user, actor.ID)

		roleChanged := in.Role != nil && *in.Role != user.Role
		deactivated := in.IsActive != nil && !*in.IsActive && user.IsActive

		if in.DisplayName != nil {
			user.DisplayName = *in.DisplayName
		}
		if in.Role != nil {
			if !in.Role.Valid() {
				return fmt.Errorf("%w: %q is not a role; the roles are admin, manager, developer", domain.ErrInvalid, *in.Role)
			}
			user.Role = *in.Role
		}
		if in.IsActive != nil {
			user.IsActive = *in.IsActive
		}
		if in.Password != nil {
			hash, err := HashPassword(*in.Password)
			if err != nil {
				return fmt.Errorf("%w: %s", domain.ErrInvalid, err)
			}
			user.PasswordHash = hash
		}

		if err := s.users.Update(ctx, tx, user); err != nil {
			return err
		}

		// A live session must not outlast the authority it was issued under.
		if deactivated || in.Password != nil || roleChanged {
			if _, err := s.sessions.RevokeAllForUser(ctx, tx, user.ID); err != nil {
				return err
			}
		}

		action := audit.ActionUserUpdated
		if roleChanged {
			action = audit.ActionUserRoleChanged
		}
		updated = user
		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    by.WritingActorID(),
			OnBehalfOf: by.OnBehalfOf(),
			Action:     action,
			SubjectID:  &user.ID,
			Before:     before,
			After:      publicUser(user, actor.ID),
		})
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteUser soft-deletes an account and revokes its sessions.
func (s *Service) DeleteUser(ctx context.Context, id uuid.UUID, by policy.Principal) error {
	if by.GlobalRole != policy.RoleAdmin {
		return adminOnly(by, "remove an account")
	}
	if id == by.UserID {
		return fmt.Errorf("%w: you cannot remove your own account; ask another admin", domain.ErrInvalid)
	}

	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		user, err := s.users.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		actor, err := s.actors.ForUser(ctx, tx, user.ID)
		if err != nil {
			return err
		}
		if err := s.users.Delete(ctx, tx, id); err != nil {
			return err
		}
		if _, err := s.sessions.RevokeAllForUser(ctx, tx, id); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    by.WritingActorID(),
			OnBehalfOf: by.OnBehalfOf(),
			Action:     audit.ActionUserDeleted,
			SubjectID:  &id,
			Before:     publicUser(user, actor.ID),
		})
	})
}

// UserByID returns one account. Admin only.
func (s *Service) UserByID(ctx context.Context, id uuid.UUID, by policy.Principal) (*domain.User, error) {
	if by.GlobalRole != policy.RoleAdmin && by.UserID != id {
		return nil, adminOnly(by, "read another account")
	}
	return s.users.ByID(ctx, s.db.Pool, id)
}

// Querier exposes the pool for services that need to read inside their own
// transaction boundaries.
func (s *Service) Querier() store.Querier { return s.db.Pool }

// publicUser is what goes into an audit row. Never the password hash: the log
// is never pruned, and a hash written into it once is there permanently.
func publicUser(u *domain.User, actorID uuid.UUID) map[string]any {
	return map[string]any{
		"id":           u.ID,
		"email":        u.Email,
		"display_name": u.DisplayName,
		"role":         string(u.Role),
		"is_active":    u.IsActive,
		"actor_id":     actorID,
	}
}

func adminOnly(by policy.Principal, action string) *policy.Denial {
	return &policy.Denial{
		Kind:          policy.DenialForbidden,
		OwningRole:    policy.OwnerAdmin,
		EffectiveRole: by.GlobalRole,
		Route:         "ask an admin",
		Message: fmt.Sprintf("To %s, ask an admin. User management is admin-owned, and your role is %s.",
			action, by.GlobalRole),
	}
}
