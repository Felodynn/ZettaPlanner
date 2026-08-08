// Package contextsvc gathers what a turn is given and hands it to
// internal/contextbuild to be assembled.
//
// The split is the point. This package does every query; contextbuild does
// every decision about what a model sees and cannot reach a database to change
// its mind. That makes the D5 rule structural rather than a filtering step —
// there is no path from the assembler to `diagram_layout` because there is no
// path from the assembler to Postgres.
//
// It also owns indexing, because what can be retrieved and what was written are
// the same question asked at two moments. A worklog committed but not indexed
// is invisible to the planner and nothing later notices, which is why the
// embedding happens inside the writing transaction (§6.3).
package contextsvc

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Service assembles context and indexes spill.
type Service struct {
	db       *store.DB
	log      *slog.Logger
	registry *llm.Registry

	projects   repo.Projects
	members    repo.Members
	users      repo.Users
	milestones repo.Milestones
	items      repo.WorkItems
	subtasks   repo.SubTasks
	checklists repo.Checklists
	deps       repo.Dependencies
	refs       repo.EntityRefs
	deviations repo.Deviations
	notes      repo.Notes
	entities   repo.Entities
	graphs     repo.Graphs
	proposals  repo.Proposals
	digests    repo.Digests
}

// New builds the service. registry may be nil, in which case there is no
// retrieval and the assembled context says so rather than presenting an empty
// section as "nothing relevant was found".
func New(db *store.DB, log *slog.Logger, registry *llm.Registry) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, log: log, registry: registry}
}

// embedder returns the configured embedding model, or nil with the reason.
//
// The reason is a sentence, not a bool. It is printed into the assembled
// context, and "no model with the `embedding` capability is configured" tells a
// reader what to do in a way that an empty section does not.
func (s *Service) embedder() (*llm.Client, string) {
	if s.registry == nil {
		return nil, "no model registry is configured in this process"
	}
	candidates, err := s.registry.Candidates("embed")
	if err != nil || len(candidates) == 0 {
		return nil, "no model with the `embedding` capability is configured in models.yaml"
	}
	return candidates[0], ""
}

// EmbeddingModel names the model spill is embedded with, for callers that need
// to report it. Empty when none is configured.
func (s *Service) EmbeddingModel() string {
	c, _ := s.embedder()
	if c == nil {
		return ""
	}
	return c.Config().Model
}

// requireMember is the read gate. A non-member gets not_found rather than
// forbidden, for the same reason as everywhere else: distinguishing them lets
// anyone enumerate what exists.
func (s *Service) requireMember(ctx context.Context, q store.Querier, p policy.Principal, projectID uuid.UUID) error {
	if p.GlobalRole == policy.RoleAdmin {
		_, err := s.projects.ByID(ctx, q, projectID)
		return err
	}
	role, err := s.members.RoleFor(ctx, q, projectID, p.UserID)
	if err != nil {
		return err
	}
	if role == nil {
		return fmt.Errorf("%w: project", domain.ErrNotFound)
	}
	return nil
}
