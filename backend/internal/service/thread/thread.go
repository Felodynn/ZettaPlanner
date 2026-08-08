// Package thread owns conversations, the turn queue, and the read side of
// governance.
//
// Posting a message and running a turn are deliberately separate: the message
// lands immediately and always, and the turn queues behind whatever else is
// running on the project (§5.3). A user whose message is refused because an
// agent is busy would learn to wait before typing, which is the opposite of a
// transcript.
package thread

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	th "github.com/Felodynn/ZettaPlanner/backend/internal/domain/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Waker is the agent runner, told that work arrived. Nil is supported: the
// runner polls as well, so a deployment that wires this up gets a turn starting
// in milliseconds rather than in a couple of seconds.
type Waker interface{ Wake() }

// Publisher receives the events this service emits (§7.2).
type Publisher interface {
	Publish(projectID uuid.UUID, eventType string, payload map[string]any)
}

// Service is the conversation application service.
type Service struct {
	db     *store.DB
	log    *slog.Logger
	waker  Waker
	events Publisher

	projects  repo.Projects
	members   repo.Members
	threads   repo.Threads
	messages  repo.Messages
	turns     repo.Turns
	actors    repo.Actors
	proposals repo.Proposals
	items     repo.WorkItems
	diagrams  repo.Diagrams
}

// New builds the service.
func New(db *store.DB, log *slog.Logger, waker Waker, events Publisher) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, log: log, waker: waker, events: events}
}

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

// ── threads ──────────────────────────────────────────────────────────────────

// List returns a project's threads.
//
// Every thread, for every member. Scope controls context assembly and UI
// placement, never visibility (§3.6): a manager reading a developer's turns is
// the design, not a leak.
func (s *Service) List(ctx context.Context, projectID uuid.UUID, p policy.Principal) ([]*th.Thread, error) {
	if err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.threads.ListByProject(ctx, s.db.Pool, projectID)
}

// EnsureInput names a scope.
type EnsureInput struct {
	ScopeKind th.ScopeKind
	// ScopeRef is a work item ref or a diagram slug. Ids are accepted too, but
	// nobody types one.
	ScopeRef string
}

// Ensure returns the thread for a scope, creating it on first use.
func (s *Service) Ensure(ctx context.Context, projectID uuid.UUID, in EnsureInput, p policy.Principal) (*th.Thread, error) {
	if in.ScopeKind == "" {
		in.ScopeKind = th.ScopeProject
	}
	if err := th.ValidateScopeKind(in.ScopeKind); err != nil {
		return nil, err
	}

	var out *th.Thread
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.requireMember(ctx, tx, p, projectID); err != nil {
			return err
		}
		scopeID, err := s.resolveScope(ctx, tx, projectID, in)
		if err != nil {
			return err
		}
		out, err = s.threads.Ensure(ctx, tx, &th.Thread{
			ID:        uuid.Must(uuid.NewV7()),
			ProjectID: projectID,
			ScopeKind: in.ScopeKind,
			ScopeID:   scopeID,
			CreatedAt: time.Now(),
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) resolveScope(ctx context.Context, q store.Querier, projectID uuid.UUID, in EnsureInput) (*uuid.UUID, error) {
	if in.ScopeKind == th.ScopeProject {
		// NULL, and the unique index is declared NULLS NOT DISTINCT so the one
		// project-scoped thread is actually unique rather than unconstrained.
		return nil, nil
	}
	ref := strings.TrimSpace(in.ScopeRef)
	if ref == "" {
		return nil, fmt.Errorf("%w: a %s thread needs a scope; give the work item ref or the diagram slug",
			domain.ErrInvalid, in.ScopeKind)
	}
	if id, err := uuid.Parse(ref); err == nil {
		return &id, nil
	}

	switch in.ScopeKind {
	case th.ScopeWorkItem:
		item, err := s.items.ByRef(ctx, q, projectID, strings.ToUpper(ref))
		if err != nil {
			return nil, err
		}
		return &item.ID, nil
	default:
		d, err := s.diagrams.BySlug(ctx, q, projectID, ref)
		if err != nil {
			return nil, err
		}
		return &d.ID, nil
	}
}

// Messages returns a page of a thread's transcript.
func (s *Service) Messages(ctx context.Context, threadID uuid.UUID, f repo.MessageFilter, p policy.Principal) ([]*th.Message, error) {
	t, err := s.threads.ByID(ctx, s.db.Pool, threadID)
	if err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, s.db.Pool, p, t.ProjectID); err != nil {
		return nil, err
	}
	return s.messages.List(ctx, s.db.Pool, threadID, f)
}

// ── posting ──────────────────────────────────────────────────────────────────

// PostResult is a posted message and the turn it queued.
type PostResult struct {
	Message *th.Message
	Turn    *th.Turn
}

// Post writes a message and enqueues a turn for it.
//
// Both in one transaction. A message that landed without its turn is a prompt
// nobody answers and nothing to retry from; a turn without its message is a run
// with no prompt. The queue position comes back in the response, because §5.3
// requires a queued turn to be *shown as* queued rather than merely to be.
func (s *Service) Post(ctx context.Context, threadID uuid.UUID, content string, p policy.Principal) (*PostResult, error) {
	body := strings.TrimSpace(content)
	if body == "" {
		return nil, fmt.Errorf("%w: an empty message asks nothing", domain.ErrInvalid)
	}

	var out PostResult
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		t, err := s.threads.ByID(ctx, tx, threadID)
		if err != nil {
			return err
		}
		if err := s.requireMember(ctx, tx, p, t.ProjectID); err != nil {
			return err
		}

		turnID := uuid.Must(uuid.NewV7())
		msg := &th.Message{
			ID:        uuid.Must(uuid.NewV7()),
			ThreadID:  threadID,
			ActorID:   p.ActorID,
			Role:      th.RoleUser,
			Content:   body,
			TurnID:    &turnID,
			CreatedAt: time.Now(),
		}
		if err := s.messages.Append(ctx, tx, msg); err != nil {
			return err
		}

		turn := &th.Turn{
			ID: turnID, ProjectID: t.ProjectID, ThreadID: threadID,
			RequestedBy: p.UserID, MessageID: &msg.ID,
			Status: th.TurnQueued, QueuedAt: time.Now(),
		}
		if err := s.turns.Enqueue(ctx, tx, turn); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{
			ProjectID: &t.ProjectID,
			ActorID:   p.ActorID,
			Action:    audit.ActionMessagePosted,
			SubjectID: &msg.ID,
			After:     map[string]any{"thread_id": threadID, "turn_id": turnID},
		}); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{
			ProjectID: &t.ProjectID,
			ActorID:   p.ActorID,
			Action:    audit.ActionTurnQueued,
			SubjectID: &turnID,
			After:     map[string]any{"thread_id": threadID},
		}); err != nil {
			return err
		}

		out.Message = msg
		out.Turn, err = s.turns.ByID(ctx, tx, turnID)
		return err
	})
	if err != nil {
		return nil, err
	}

	s.publish(out.Turn.ProjectID, "turn.queued", map[string]any{
		"turn_id": out.Turn.ID, "position": out.Turn.QueuePosition, "requested_by": p.UserID,
	})
	if s.waker != nil {
		s.waker.Wake()
	}
	return &out, nil
}

// ── turns ────────────────────────────────────────────────────────────────────

// Turns returns a project's turns, newest first.
func (s *Service) Turns(ctx context.Context, projectID uuid.UUID, limit int, p policy.Principal) ([]*th.Turn, error) {
	if err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.turns.ListByProject(ctx, s.db.Pool, projectID, limit)
}

// Turn returns one turn.
func (s *Service) Turn(ctx context.Context, id uuid.UUID, p policy.Principal) (*th.Turn, error) {
	t, err := s.turns.ByID(ctx, s.db.Pool, id)
	if err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, s.db.Pool, p, t.ProjectID); err != nil {
		return nil, err
	}
	return t, nil
}

// Trace returns a turn's events. The debugging surface behind
// `zettaplanner trace`.
func (s *Service) Trace(ctx context.Context, id uuid.UUID, p policy.Principal) (*th.Turn, []repo.TurnEvent, []*th.Message, error) {
	t, err := s.Turn(ctx, id, p)
	if err != nil {
		return nil, nil, nil, err
	}
	events, err := s.turns.Events(ctx, s.db.Pool, id)
	if err != nil {
		return nil, nil, nil, err
	}
	messages, err := s.messages.ForTurn(ctx, s.db.Pool, id)
	if err != nil {
		return nil, nil, nil, err
	}
	return t, events, messages, nil
}

// Cancel stops a queued or running turn.
//
// Anybody on the project may cancel, not only whoever asked: a turn holds the
// project's only slot, and requiring the person who started it to be present is
// how a queue stays blocked all afternoon.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID, p policy.Principal) (*th.Turn, error) {
	var out *th.Turn
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		t, err := s.turns.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.requireMember(ctx, tx, p, t.ProjectID); err != nil {
			return err
		}
		if t.Status.Terminal() {
			return fmt.Errorf("%w: that turn already ended as %s", domain.ErrConflict, t.Status)
		}
		if _, err := s.turns.Cancel(ctx, tx, id); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{
			ProjectID: &t.ProjectID,
			ActorID:   p.ActorID,
			Action:    audit.ActionTurnCancelled,
			SubjectID: &id,
			Before:    map[string]any{"status": string(t.Status)},
			After:     map[string]any{"status": string(th.TurnCancelled)},
		}); err != nil {
			return err
		}
		out, err = s.turns.ByID(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish(out.ProjectID, "turn.ended", map[string]any{
		"turn_id": out.ID, "status": string(out.Status), "finish_reason": string(out.Finish),
	})
	return out, nil
}

// ── proposals (read side; Phase 7 decides them) ──────────────────────────────

// Proposals returns a project's proposals.
func (s *Service) Proposals(ctx context.Context, projectID uuid.UUID, status pr.Status, p policy.Principal) ([]*pr.Proposal, error) {
	if err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.proposals.ListByProject(ctx, s.db.Pool, projectID, status)
}

// Proposal returns one proposal with its changes.
func (s *Service) Proposal(ctx context.Context, id uuid.UUID, p policy.Principal) (*pr.Proposal, error) {
	prop, err := s.proposals.ByID(ctx, s.db.Pool, id)
	if err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, s.db.Pool, p, prop.ProjectID); err != nil {
		return nil, err
	}
	return prop, nil
}

func (s *Service) publish(projectID uuid.UUID, kind string, payload map[string]any) {
	if s.events != nil {
		s.events.Publish(projectID, kind, payload)
	}
}
