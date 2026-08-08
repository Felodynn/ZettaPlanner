// Package governance decides and applies proposals, and runs re-evaluate.
//
// Phase 6 raises proposals; this is where they are decided. Four rules run
// through every file here.
//
//  1. **Atomic or nothing** (§5.1). A proposal is one revision or none. If any
//     change conflicts, the whole proposal becomes `superseded` and the
//     conflict is reported. Never partially applied, never auto-merged —
//     merging is the judgement the human was being asked to make.
//
//  2. **A rejection requires a rationale** (§5.2). Without it the agent
//     re-proposes what was already refused and the person concludes the tool
//     does not listen. The rationale is not a courtesy: it is the `dead_ends`
//     field of the compaction summary in durable form, and it goes straight
//     into assembled context.
//
//  3. **Approval authority is the ownership table, not a second rulebook.**
//     Managers and admins may approve anything; a developer may approve only a
//     proposal whose every change they could have written themselves. That is
//     decided by running the same CheckWrite the direct path runs, with the
//     approver's own standing.
//
//  4. **Re-evaluate returns a proposal, never a write** (§5.4) — including when
//     a human triggers it. A stray click must not rewrite a plan somebody
//     corrected by hand.
package governance

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Publisher receives the events this service emits (§7.2).
type Publisher interface {
	Publish(projectID uuid.UUID, eventType string, payload map[string]any)
}

// Registry resolves the model re-evaluate runs on.
type Registry interface {
	Candidates(role string) ([]*llm.Client, error)
}

// Service is the governance application service.
type Service struct {
	db       *store.DB
	log      *slog.Logger
	events   Publisher
	registry Registry

	plan     *plansvc.Service
	diagrams *diagramsvc.Service

	projects   repo.Projects
	members    repo.Members
	proposals  repo.Proposals
	milestones repo.Milestones
	items      repo.WorkItems
	subtasks   repo.SubTasks
	diagramRep repo.Diagrams
	ingests    repo.Ingests
	actors     repo.Actors
}

// New builds the service. registry may be nil, which disables re-evaluate and
// says so rather than failing obscurely.
func New(db *store.DB, log *slog.Logger, plan *plansvc.Service, diagrams *diagramsvc.Service,
	events Publisher, registry Registry) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, log: log, events: events, registry: registry, plan: plan, diagrams: diagrams}
}

func (s *Service) publish(projectID uuid.UUID, kind string, payload map[string]any) {
	if s.events != nil {
		s.events.Publish(projectID, kind, payload)
	}
}

// requireMember is the read gate: a non-member gets not_found rather than
// forbidden, for the same reason as everywhere else — distinguishing them lets
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

// subject resolves the caller's standing on a project, with no proposal
// authority attached. This is deliberately the *plain* subject: it is what the
// approval gate asks "could you have written this yourself".
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
