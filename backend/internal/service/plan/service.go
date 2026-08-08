// Package plan owns milestones, work items, subtasks, dependencies,
// checklists, worklogs and deviations.
//
// Three rules run through every file here.
//
//  1. **Ownership is checked per field, before anything is written.** The
//     handler supplies the set of wire field names the request set; this
//     package never names an individual field when deciding. Adding a field to
//     CONTRACT §4.2 is therefore the whole change needed to enforce it.
//
//  2. **Every state change is its own audit action.** A status move writes
//     `*.status_changed` with before and after, not `*.updated`. Bottleneck
//     analysis is almost entirely transition analysis, and a transition
//     recorded as a generic edit is a transition the v0.1 export cannot see
//     (§6.5 constraint 2). The same PATCH may therefore produce several
//     entries — a status move, a reassignment and a retitle are three facts —
//     and they share one request_id, which is what groups them back into one
//     operation.
//
//  3. **Nothing here derives anything.** No duration is stored, no warning is
//     stored, no date cascades, and no entity reference is inferred. Every one
//     of those is a second source of truth that disagrees with the first under
//     exactly the conditions nobody tests.
package plan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Indexer writes a worklog into the vector store, inside the transaction that
// wrote the worklog.
//
// An interface here rather than a dependency on the context service, so the
// plan does not import the thing that reads the plan. Nil is a supported
// configuration — a deployment with no embedding model still records work; what
// it loses is context assembly's third layer, and the assembled context says so
// rather than showing an empty section.
//
// When it is **not** nil and fails, the write fails with it. A worklog that
// committed but did not index is invisible to the planner with nothing to show
// that it happened, and "configured and broken" is a different situation from
// "not configured" (D47).
type Indexer interface {
	IndexWorklog(ctx context.Context, q store.Querier, w *pl.Worklog) error
}

// Service is the plan application service.
type Service struct {
	db      *store.DB
	log     *slog.Logger
	indexer Indexer

	projects   repo.Projects
	members    repo.Members
	milestones repo.Milestones
	items      repo.WorkItems
	subtasks   repo.SubTasks
	deps       repo.Dependencies
	refs       repo.EntityRefs
	checklists repo.Checklists
	worklogs   repo.Worklogs
	deviations repo.Deviations
	warnings   repo.Warnings
	users      repo.Users
	entities   repo.Entities
	dashboards repo.Dashboards
}

// New builds the service. indexer may be nil.
func New(db *store.DB, log *slog.Logger, indexer Indexer) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, log: log, indexer: indexer}
}

// ── writers ──────────────────────────────────────────────────────────────────

// Writer is who a write is made by, and under what authority.
//
// Every mutating method here has an `InTx` form taking one of these, because a
// proposal applies several plan changes **as one revision or not at all** (§5.1)
// and so cannot be several transactions. The public methods are thin wrappers
// that open the transaction and pass By(p).
type Writer struct {
	policy.Principal

	// Proposal is the approved proposal this write is applying.
	//
	// It reaches policy.Subject and is what lets an approved proposal change a
	// field its approver could not have patched directly. Only the governance
	// service sets it, and only after deciding who was allowed to approve.
	Proposal *uuid.UUID
}

// By is an ordinary write, under no proposal.
func By(p policy.Principal) Writer { return Writer{Principal: p} }

// UnderProposal is a write applying an approved proposal.
func UnderProposal(p policy.Principal, proposalID uuid.UUID) Writer {
	return Writer{Principal: p, Proposal: &proposalID}
}

// ── subjects ─────────────────────────────────────────────────────────────────

// subject resolves the caller's standing on a project, with no relationship to
// any particular row. Used for creates and for project-level reads.
func (s *Service) subject(ctx context.Context, q store.Querier, w Writer, projectID uuid.UUID) (policy.Subject, error) {
	override, err := s.members.RoleFor(ctx, q, projectID, w.UserID)
	if err != nil {
		return policy.Subject{}, err
	}
	return policy.Subject{
		Principal:        w.Principal,
		ProjectID:        projectID,
		EffectiveRole:    policy.Effective(w.GlobalRole, override),
		IsMember:         override != nil,
		ApprovedProposal: w.Proposal,
	}, nil
}

// forItem resolves the caller's standing on a work item, setting IsAssignee.
//
// The flag is computed from the row that was actually loaded, in the same
// transaction as the write it authorises. Resolving it anywhere else — from a
// cached copy, from the request body — would mean the check is about a
// different item from the one being written.
func (s *Service) forItem(ctx context.Context, q store.Querier, w Writer, item *pl.WorkItem) (policy.Subject, error) {
	sub, err := s.subject(ctx, q, w, item.ProjectID)
	if err != nil {
		return policy.Subject{}, err
	}
	sub.IsAssignee = item.AssigneeID != nil && *item.AssigneeID == w.UserID
	return sub, nil
}

// requireMember is the read gate. A caller who is not a member gets not_found
// rather than forbidden, for the same reason as projects: distinguishing them
// would let anyone enumerate what exists.
func (s *Service) requireMember(ctx context.Context, q store.Querier, p policy.Principal, projectID uuid.UUID) error {
	if p.GlobalRole == policy.RoleAdmin {
		if _, err := s.projects.ByID(ctx, q, projectID); err != nil {
			return err
		}
		return nil
	}
	override, err := s.members.RoleFor(ctx, q, projectID, p.UserID)
	if err != nil {
		return err
	}
	if override == nil {
		return fmt.Errorf("%w: project", domain.ErrNotFound)
	}
	return nil
}

// ── shared helpers ───────────────────────────────────────────────────────────

// provenanceFor is the origin a write carries (§3.7).
//
// An agent's direct write is `agent_committed`, never `human_edited`: the whole
// value of the field is that a later reader can tell which changes a person
// made. A human touching something a model produced is `human_edited`, which is
// the highest-signal input the re-evaluate flow will ever get (§5.4).
func provenanceFor(p policy.Principal, existing domain.Provenance, creating bool) domain.Provenance {
	if p.IsAgent() {
		return domain.ProvAgentCommitted
	}
	if creating {
		return domain.ProvHumanCreated
	}
	if existing == domain.ProvHumanCreated {
		return domain.ProvHumanCreated
	}
	return domain.ProvHumanEdited
}

// assigneeMustBeAMember rejects assigning work to somebody who cannot see it.
//
// Not a foreign key check — the column already has one. This is the rule that
// an assignee is a member of the project, which the schema cannot express and
// which is otherwise discovered when the assignee opens the item and gets a
// 404.
func (s *Service) assigneeMustBeAMember(ctx context.Context, q store.Querier, projectID uuid.UUID, userID *uuid.UUID) error {
	if userID == nil {
		return nil
	}
	user, err := s.users.ByID(ctx, q, *userID)
	if err != nil {
		return err
	}
	if user.Role == policy.RoleAdmin {
		return nil
	}
	role, err := s.members.RoleFor(ctx, q, projectID, *userID)
	if err != nil {
		return err
	}
	if role == nil {
		return fmt.Errorf("%w: %s is not a member of this project, so assigning work to them would give them "+
			"an item they cannot open. Add them to the project first",
			domain.ErrInvalid, user.DisplayName)
	}
	return nil
}

// isNotFound reports whether err is the domain's absence sentinel. Used where a
// dangling reference is a normal answer rather than a failure — a work item
// whose milestone has since been deleted still has to render.
func isNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }

// changed reports whether a patch actually moves a value. A PATCH that sets
// status to the value it already holds is not a transition, and recording one
// would put a state change in the log that never happened.
func changed[T comparable](before, after T) bool { return before != after }

func sameUUID(a, b *uuid.UUID) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

func sameDate(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(*b)
	}
}
