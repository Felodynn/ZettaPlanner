package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Actors is the actors aggregate.
//
// Actors are never deleted, soft or otherwise. Every audit row references one,
// and the log's retention is never (§6.5 constraint 5); an actor that could
// disappear would make its own entries unreadable.
type Actors struct{}

const actorColumns = `id, kind, user_id, agent_name, model_ref, created_at`

// Create inserts an actor.
func (Actors) Create(ctx context.Context, q store.Querier, a *domain.Actor) error {
	const sql = `
		INSERT INTO actors (id, kind, user_id, agent_name, model_ref, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := q.Exec(ctx, sql,
		a.ID, string(a.Kind), a.UserID, nullIfEmpty(a.AgentName), nullIfEmpty(a.ModelRef), a.CreatedAt)
	return conflict(err, "that principal already has an actor")
}

// ByID returns one actor.
func (Actors) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*domain.Actor, error) {
	const sql = `SELECT ` + actorColumns + ` FROM actors WHERE id = $1`
	return scanActor(q.QueryRow(ctx, sql, id))
}

// ForUser returns the human actor belonging to a user.
func (Actors) ForUser(ctx context.Context, q store.Querier, userID uuid.UUID) (*domain.Actor, error) {
	const sql = `SELECT ` + actorColumns + ` FROM actors WHERE user_id = $1 AND kind = 'human'`
	return scanActor(q.QueryRow(ctx, sql, userID))
}

// Agent returns the actor for one named agent on one model, creating it if this
// deployment has none.
//
// **Agents are real principals** (D11), so they get an actor row of their own
// rather than borrowing the user's. The audit entry then records both: the
// agent in `actor_id`, the instructing user in `on_behalf_of`. Keyed by model
// as well as name because "the planner" running glm-5.2 and the same planner
// running a local fallback are different enough that a retrospective wants to
// tell them apart.
func (Actors) Agent(ctx context.Context, q store.Querier, name, modelRef string) (*domain.Actor, error) {
	const find = `SELECT ` + actorColumns + ` FROM actors
	               WHERE kind = 'agent' AND agent_name = $1 AND model_ref = $2 LIMIT 1`
	a, err := scanActor(q.QueryRow(ctx, find, name, modelRef))
	if err == nil {
		return a, nil
	}
	if !isNotFound(err) {
		return nil, err
	}

	const create = `
		INSERT INTO actors (id, kind, agent_name, model_ref, created_at)
		VALUES ($1, 'agent', $2, $3, now())`
	if _, err := q.Exec(ctx, create, uuid.Must(uuid.NewV7()), name, modelRef); err != nil {
		return nil, err
	}
	return scanActor(q.QueryRow(ctx, find, name, modelRef))
}

// System returns the system actor, creating it if this deployment has none.
//
// Migrations and ingest scaffolding write as `system`, and those writes need a
// principal that exists before any human does.
func (Actors) System(ctx context.Context, q store.Querier) (*domain.Actor, error) {
	const find = `SELECT ` + actorColumns + ` FROM actors WHERE kind = 'system' ORDER BY created_at LIMIT 1`
	a, err := scanActor(q.QueryRow(ctx, find))
	if err == nil {
		return a, nil
	}

	const create = `
		INSERT INTO actors (id, kind, created_at) VALUES ($1, 'system', now())
		ON CONFLICT DO NOTHING`
	if _, err := q.Exec(ctx, create, uuid.Must(uuid.NewV7())); err != nil {
		return nil, err
	}
	return scanActor(q.QueryRow(ctx, find))
}

func scanActor(row scanner) (*domain.Actor, error) {
	var (
		a         domain.Actor
		kind      string
		agentName *string
		modelRef  *string
	)
	if err := row.Scan(&a.ID, &kind, &a.UserID, &agentName, &modelRef, &a.CreatedAt); err != nil {
		return nil, notFound(err, "actor")
	}
	a.Kind = domain.ActorKind(kind)
	if agentName != nil {
		a.AgentName = *agentName
	}
	if modelRef != nil {
		a.ModelRef = *modelRef
	}
	return &a, nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
