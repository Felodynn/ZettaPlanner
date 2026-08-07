package diagram

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
)

// EntityDetail is an entity together with everywhere it appears and everything
// it is connected to, across every diagram in the project.
//
// This is what makes a CatalogEntity an identity rather than a box in one
// picture (§3.4): "what depends on the token store" is answerable for the
// project, not per-diagram.
type EntityDetail struct {
	Entity     *dg.Entity
	Placements []repo.Placement
	Dependents []repo.Related
	DependsOn  []repo.Related
	Notes      []*dg.Note
}

// Entities lists a project's catalog.
func (s *Service) Entities(ctx context.Context, projectID uuid.UUID, p policy.Principal) ([]*dg.Entity, error) {
	if _, err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.entities.ListByProject(ctx, s.db.Pool, projectID)
}

// Entity returns one entity with its cross-diagram graph neighbourhood.
//
// depth bounds the traversal. The default is two hops, which is where a human
// reading the answer usually stops; the recursion is hard-capped regardless,
// because the architecture graph is allowed to contain cycles and the traversal
// is not allowed to follow one.
func (s *Service) Entity(ctx context.Context, entityID uuid.UUID, depth int, p policy.Principal) (*EntityDetail, error) {
	entity, err := s.entities.ByID(ctx, s.db.Pool, entityID)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireMember(ctx, s.db.Pool, p, entity.ProjectID); err != nil {
		return nil, err
	}

	placements, err := s.entities.Placements(ctx, s.db.Pool, entityID)
	if err != nil {
		return nil, err
	}
	dependents, err := s.graphs.Dependents(ctx, s.db.Pool, entity.ProjectID, entityID, depth)
	if err != nil {
		return nil, err
	}
	dependsOn, err := s.graphs.DependsOn(ctx, s.db.Pool, entity.ProjectID, entityID, depth)
	if err != nil {
		return nil, err
	}
	notes, err := s.notes.ForDiagram(ctx, s.db.Pool,
		&dg.Diagram{ProjectID: entity.ProjectID, ID: uuid.Nil}, []uuid.UUID{entityID})
	if err != nil {
		return nil, err
	}

	return &EntityDetail{
		Entity: entity, Placements: placements,
		Dependents: dependents, DependsOn: dependsOn, Notes: notes,
	}, nil
}

// ── notes ────────────────────────────────────────────────────────────────────

// Notes lists a project's notes, optionally filtered by status.
func (s *Service) Notes(ctx context.Context, projectID uuid.UUID, status dg.NoteStatus, p policy.Principal) ([]*dg.Note, error) {
	if _, err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.notes.ListByProject(ctx, s.db.Pool, projectID, status)
}

// SetNoteStatus moves a note through its lifecycle.
//
// Resolving "these two may be the same datastore" is an architectural
// judgement, so it is manager-owned (D31) — checked through the ownership table
// like everything else rather than by a bespoke check here.
func (s *Service) SetNoteStatus(ctx context.Context, noteID uuid.UUID, status dg.NoteStatus, p policy.Principal) (*dg.Note, error) {
	if !status.Valid() {
		return nil, &dg.ValidationError{Message: "note status must be one of open, resolved, dismissed"}
	}

	var out *dg.Note
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		note, err := s.notes.ByID(ctx, tx, noteID)
		if err != nil {
			return err
		}
		subject, err := s.requireMember(ctx, tx, p, note.ProjectID)
		if err != nil {
			return err
		}
		if den := policy.CheckWrite(policy.KindNote, []string{"status"}, subject); den != nil {
			return den
		}
		if note.Status == status {
			out = note
			return nil
		}

		before := string(note.Status)
		if err := s.notes.SetStatus(ctx, tx, noteID, status); err != nil {
			return err
		}
		note.Status = status
		out = note

		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &note.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionNoteStatusChanged, SubjectID: &noteID,
			Before: map[string]any{"status": before},
			After:  map[string]any{"status": string(status)},
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
