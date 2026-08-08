// Package agent is the planner loop: the tool inventory, the turn lease, the
// hard limits, the retry policy, and the trace.
//
// Three rules run through it.
//
//  1. **The tool inventory is the enforcement mechanism.** There is no tool
//     that writes a manager-owned field. Not one that refuses — one that does
//     not exist. A prompt saying "do not change due dates" is a request; an
//     inventory without such a tool is a fact, and it survives a confused
//     model, an adversarial one, and a truncated system prompt.
//
//  2. **A turn that produced nothing is not a success.** No write, no
//     proposal, no handoff means `no_output`, said out loud. A run that
//     anticipates a wall and stops looks identical to a completed one
//     otherwise — the gap LumixAgents Phase 6 found and did not close.
//
//  3. **Everything is persisted.** Every model call, every tool call, every
//     denial, every retry. `zettaplanner trace <turn_id>` replays it. A turn
//     whose behaviour cannot be reconstructed is a turn whose behaviour cannot
//     be fixed.
package agent

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	th "github.com/Felodynn/ZettaPlanner/backend/internal/domain/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/contextsvc"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// Name is the agent's identity in the actors table and in every audit row it
// writes. One name for v0: a second agent is a second set of measurements, and
// Phase 6's whole point is measuring one harness.
const Name = "planner"

// Publisher receives the events a turn emits (§7.2). Nil is fine: a deployment
// with no stream still runs turns, and the transcript is the record either way.
type Publisher interface {
	Publish(projectID uuid.UUID, eventType string, payload map[string]any)
}

// Deps are the collaborators a turn needs.
type Deps struct {
	DB       *store.DB
	Log      *slog.Logger
	Registry *llm.Registry
	Context  *contextsvc.Service
	Plan     *plansvc.Service
	Diagrams *diagramsvc.Service
	Events   Publisher
	Limits   Limits

	threads   repo.Threads
	messages  repo.Messages
	turns     repo.Turns
	actors    repo.Actors
	users     repo.Users
	projects  repo.Projects
	members   repo.Members
	proposals repo.Proposals
	notes     repo.Notes
	items     repo.WorkItems
	msRepo    repo.Milestones
	entities  repo.Entities
}

// Runner claims queued turns and runs them.
type Runner struct {
	deps  Deps
	tools *toolset

	wg   sync.WaitGroup
	stop chan struct{}
	// wake lets a freshly enqueued turn start without waiting for the next
	// poll. The poll is the safety net, not the mechanism.
	wake chan struct{}
	once sync.Once
}

// pollInterval is the safety net behind the wake channel: a turn enqueued by
// another process, or one whose wake was dropped because the channel was full,
// still starts within this.
const pollInterval = 2 * time.Second

// heartbeatInterval keeps a running turn's lease alive and is also how often
// the loop notices it was cancelled.
const heartbeatInterval = 10 * time.Second

// abandonAfter is how long a running turn may go without a heartbeat before it
// is declared abandoned. Generous relative to the interval: a model call that
// takes four minutes is normal, and reaping a live turn is worse than waiting.
const abandonAfter = 3 * time.Minute

// New builds the runner.
func New(deps Deps) *Runner {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	deps.Limits = deps.Limits.normalise()
	return &Runner{
		deps:  deps,
		tools: newToolset(inventory()),
		stop:  make(chan struct{}),
		wake:  make(chan struct{}, 1),
	}
}

// Tools exposes the inventory, for the OpenAPI surface and for tests that
// assert no write tool reaches a manager-owned field.
func (r *Runner) Tools() []Tool { return inventory() }

// Start begins claiming turns. The context is the process's, not a request's.
func (r *Runner) Start(ctx context.Context) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.dispatch(ctx)
	}()
}

// Stop asks the runner to finish and waits for the turn in flight.
//
// Waits rather than cancels: a turn killed mid-write leaves a proposal half
// raised, and the whole point of the lease is that somebody can tell what
// happened afterwards.
func (r *Runner) Stop() {
	r.once.Do(func() { close(r.stop) })
	r.wg.Wait()
}

// Wake tells the runner a turn was just enqueued.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) dispatch(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.wake:
		}

		if err := r.reap(ctx); err != nil {
			r.deps.Log.WarnContext(ctx, "reaping abandoned turns", "error", err)
		}
		for {
			started, err := r.claimOne(ctx)
			if err != nil {
				r.deps.Log.WarnContext(ctx, "claiming a turn", "error", err)
				break
			}
			if !started {
				break
			}
		}
	}
}

// claimOne runs at most one turn and reports whether it found work.
func (r *Runner) claimOne(ctx context.Context) (bool, error) {
	projects, err := r.deps.turns.ProjectsWithQueuedTurns(ctx, r.deps.DB.Pool)
	if err != nil || len(projects) == 0 {
		return false, err
	}

	for _, projectID := range projects {
		turn, err := r.deps.turns.ClaimNext(ctx, r.deps.DB.Pool, projectID)
		if errors.Is(err, repo.ErrTurnRunning) || errors.Is(err, domain.ErrNotFound) {
			continue // somebody else got there first, or the queue drained
		}
		if err != nil {
			return false, err
		}

		// A fresh request id per turn: it groups every audit row the turn
		// writes, which is what makes "what did that turn do" one query.
		turnCtx := telemetry.WithRequestID(context.WithoutCancel(ctx), telemetry.NewRequestID())
		r.wg.Add(1)
		func() {
			defer r.wg.Done()
			r.runTurn(turnCtx, turn)
		}()
		return true, nil
	}
	return false, nil
}

// reap marks turns whose process died as abandoned.
func (r *Runner) reap(ctx context.Context) error {
	ids, err := r.deps.turns.Reap(ctx, r.deps.DB.Pool, abandonAfter)
	if err != nil || len(ids) == 0 {
		return err
	}
	for _, id := range ids {
		r.deps.Log.WarnContext(ctx, "turn abandoned; its lease lapsed", "turn", id)
		if turn, err := r.deps.turns.ByID(ctx, r.deps.DB.Pool, id); err == nil {
			r.publish(turn.ProjectID, "turn.ended", map[string]any{
				"turn_id": turn.ID, "status": string(turn.Status), "finish_reason": string(turn.Finish),
			})
		}
	}
	return nil
}

// RecoverInterrupted sweeps turns left running by a killed process, at boot.
//
// A turn claiming to be in flight blocks its project's queue for as long as the
// lease would take to lapse, and after a restart nothing is going to heartbeat
// it. Swept once before anything can look.
func (r *Runner) RecoverInterrupted(ctx context.Context) (int, error) {
	ids, err := r.deps.turns.Reap(ctx, r.deps.DB.Pool, 0)
	return len(ids), err
}

func (r *Runner) publish(projectID uuid.UUID, kind string, payload map[string]any) {
	if r.deps.Events == nil {
		return
	}
	r.deps.Events.Publish(projectID, kind, payload)
}

// auditTurn writes one audit row for a turn transition.
func (r *Runner) auditTurn(ctx context.Context, turn *th.Turn, action audit.Action, after map[string]any) {
	err := r.deps.DB.InTx(ctx, func(tx pgx.Tx) error {
		actor, err := r.deps.actors.Agent(ctx, tx, Name, turn.Model)
		if err != nil {
			return err
		}
		user := turn.RequestedBy
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &turn.ProjectID,
			ActorID:    actor.ID,
			OnBehalfOf: &user,
			Action:     action,
			SubjectID:  &turn.ID,
			After:      after,
		})
	})
	if err != nil {
		r.deps.Log.WarnContext(ctx, "recording a turn transition", "turn", turn.ID, "action", action, "error", err)
	}
}
