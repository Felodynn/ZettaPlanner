package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	th "github.com/Felodynn/ZettaPlanner/backend/internal/domain/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Turns is the agent_turns aggregate.
//
// **Serialization is a database constraint, not a mutex** (§5.3). 0001 declares
// `CREATE UNIQUE INDEX agent_turns_one_running ON agent_turns (project_id)
// WHERE status = 'running'`, so two processes racing to start a turn on the
// same project cannot both win — one gets a unique violation and reads it as
// "somebody else is running". A mutex in Go would hold only within one process,
// and this system is meant to survive being run twice.
type Turns struct{}

const turnColumns = `t.id, t.project_id, t.thread_id, t.requested_by, t.message_id, t.status,
                     coalesce(t.finish_reason, ''), coalesce(t.model, ''),
                     coalesce(t.tokens_in, 0), coalesce(t.tokens_out, 0), coalesce(t.turns, 0),
                     t.retries, coalesce(t.wall_ms, 0),
                     coalesce(t.error_class, ''), coalesce(t.error_detail, ''),
                     coalesce(t.queued_at, now()), t.heartbeat_at, t.started_at, t.ended_at`

// Enqueue adds a turn to the back of a project's queue.
func (Turns) Enqueue(ctx context.Context, q store.Querier, t *th.Turn) error {
	const sql = `
		INSERT INTO agent_turns (id, project_id, thread_id, requested_by, message_id, status, queued_at)
		VALUES ($1, $2, $3, $4, $5, 'queued', $6)`
	_, err := q.Exec(ctx, sql, t.ID, t.ProjectID, t.ThreadID, t.RequestedBy, t.MessageID, t.QueuedAt)
	return err
}

// ErrTurnRunning reports that the project's single slot is taken.
//
// A sentinel rather than an error string, because the caller's correct response
// is to wait rather than to fail: the turn stays queued and will be claimed
// when the current one ends.
var ErrTurnRunning = errors.New("a turn is already running on this project")

// ClaimNext moves the oldest queued turn to running, or returns ErrTurnRunning.
//
// `FOR UPDATE SKIP LOCKED` on the candidate means two runners racing pick
// different rows rather than blocking on each other; the unique index then
// rejects whichever one would have been the second running turn. Both
// mechanisms are needed: the skip-lock keeps the queue moving, the index keeps
// the invariant.
func (Turns) ClaimNext(ctx context.Context, q store.Querier, projectID uuid.UUID) (*th.Turn, error) {
	const sql = `
		UPDATE agent_turns SET status = 'running', started_at = now(), heartbeat_at = now()
		 WHERE id = (
		   SELECT id FROM agent_turns
		    WHERE project_id = $1 AND status = 'queued'
		    ORDER BY queued_at, id
		    LIMIT 1
		    FOR UPDATE SKIP LOCKED)
		RETURNING id`
	var id uuid.UUID
	err := q.QueryRow(ctx, sql, projectID).Scan(&id)
	if isNoRows(err) {
		return nil, missing("queued turn")
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrTurnRunning
		}
		return nil, err
	}
	return Turns{}.ByID(ctx, q, id)
}

// Heartbeat keeps a running turn's lease alive.
//
// Returns false when the row is no longer running, which is how a loop finds
// out it was cancelled or reaped without polling a second thing.
func (Turns) Heartbeat(ctx context.Context, q store.Querier, id uuid.UUID) (bool, error) {
	const sql = `UPDATE agent_turns SET heartbeat_at = now() WHERE id = $1 AND status = 'running'`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// TurnResult is what a finished turn recorded about itself.
type TurnResult struct {
	Status      th.TurnStatus
	Finish      th.FinishReason
	Model       string
	TokensIn    int
	TokensOut   int
	Steps       int
	Retries     int
	WallMS      int
	ErrorClass  string
	ErrorDetail string
}

// Finish closes a running turn. Guarded on `running`, so a turn cancelled
// underneath the loop stays cancelled: the loop's own verdict does not
// overwrite the human's.
func (Turns) Finish(ctx context.Context, q store.Querier, id uuid.UUID, r TurnResult) (bool, error) {
	const sql = `
		UPDATE agent_turns
		   SET status = $2, finish_reason = $3, model = $4, tokens_in = $5, tokens_out = $6,
		       turns = $7, retries = $8, wall_ms = $9, error_class = $10, error_detail = $11,
		       ended_at = now()
		 WHERE id = $1 AND status = 'running'`
	tag, err := q.Exec(ctx, sql, id, string(r.Status), string(r.Finish), nullIfEmpty(r.Model),
		r.TokensIn, r.TokensOut, r.Steps, r.Retries, r.WallMS,
		nullIfEmpty(r.ErrorClass), nullIfEmpty(r.ErrorDetail))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Cancel stops a queued or running turn.
//
// A running turn is marked immediately rather than asked politely: the loop
// checks its own status on every step and stops, and if its process has already
// died the row is correct anyway. Cancellation is a real outcome, not a flavour
// of failure — a user who stops a turn did not hit a bug.
func (Turns) Cancel(ctx context.Context, q store.Querier, id uuid.UUID) (th.TurnStatus, error) {
	const sql = `
		UPDATE agent_turns
		   SET status = 'cancelled', finish_reason = 'cancelled', ended_at = now()
		 WHERE id = $1 AND status IN ('queued', 'running')
		RETURNING (SELECT status FROM agent_turns WHERE id = $1)`
	var previous string
	err := q.QueryRow(ctx, sql, id).Scan(&previous)
	if isNoRows(err) {
		return "", missing("live turn")
	}
	if err != nil {
		return "", err
	}
	return th.TurnStatus(previous), nil
}

// Reap marks running turns whose heartbeat has lapsed as abandoned.
//
// An abandoned turn's partial output is retained as transcript and applies
// nothing (§5.3). Not restarted: a turn nobody is waiting for any more is
// model budget spent on an answer nobody will read.
func (Turns) Reap(ctx context.Context, q store.Querier, olderThan time.Duration) ([]uuid.UUID, error) {
	const sql = `
		UPDATE agent_turns
		   SET status = 'abandoned', finish_reason = 'abandoned', ended_at = now(),
		       error_class = coalesce(error_class, 'interrupted')
		 WHERE status = 'running' AND heartbeat_at < now() - $1::interval
		RETURNING id`
	rows, err := q.Query(ctx, sql, olderThan.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ByID returns one turn with its queue position.
func (Turns) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*th.Turn, error) {
	const sql = `
		SELECT ` + turnColumns + `, coalesce(u.display_name, ''),
		       CASE WHEN t.status = 'queued' THEN (
		         SELECT count(*) + 1 FROM agent_turns o
		          WHERE o.project_id = t.project_id AND o.status = 'queued' AND o.queued_at < t.queued_at)
		       ELSE 0 END
		  FROM agent_turns t
		  LEFT JOIN users u ON u.id = t.requested_by
		 WHERE t.id = $1`
	return scanTurn(q.QueryRow(ctx, sql, id))
}

// ListByProject returns a project's turns, newest first, plus every live one's
// queue position.
func (Turns) ListByProject(ctx context.Context, q store.Querier, projectID uuid.UUID, limit int) ([]*th.Turn, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const sql = `
		SELECT ` + turnColumns + `, coalesce(u.display_name, ''),
		       CASE WHEN t.status = 'queued' THEN (
		         SELECT count(*) + 1 FROM agent_turns o
		          WHERE o.project_id = t.project_id AND o.status = 'queued' AND o.queued_at < t.queued_at)
		       ELSE 0 END
		  FROM agent_turns t
		  LEFT JOIN users u ON u.id = t.requested_by
		 WHERE t.project_id = $1
		 ORDER BY coalesce(t.queued_at, t.started_at) DESC, t.id DESC
		 LIMIT $2`
	rows, err := q.Query(ctx, sql, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*th.Turn
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ProjectsWithQueuedTurns lists the projects that have work waiting, so the
// runner does not poll every project in the deployment.
func (Turns) ProjectsWithQueuedTurns(ctx context.Context, q store.Querier) ([]uuid.UUID, error) {
	const sql = `
		SELECT DISTINCT project_id FROM agent_turns
		 WHERE status = 'queued'
		   AND project_id NOT IN (SELECT project_id FROM agent_turns WHERE status = 'running')`
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func scanTurn(row scanner) (*th.Turn, error) {
	var (
		t      th.Turn
		status string
		finish string
	)
	err := row.Scan(&t.ID, &t.ProjectID, &t.ThreadID, &t.RequestedBy, &t.MessageID, &status, &finish,
		&t.Model, &t.TokensIn, &t.TokensOut, &t.Steps, &t.Retries, &t.WallMS,
		&t.ErrorClass, &t.ErrorDetail, &t.QueuedAt, &t.HeartbeatAt, &t.StartedAt, &t.EndedAt,
		&t.RequestedByName, &t.QueuePosition)
	if err != nil {
		return nil, notFound(err, "turn")
	}
	t.Status = th.TurnStatus(status)
	t.Finish = th.FinishReason(finish)
	return &t, nil
}

// ── the trace ────────────────────────────────────────────────────────────────

// TurnEvent is one thing that happened inside a turn.
type TurnEvent struct {
	ID         uuid.UUID
	TurnID     uuid.UUID
	Seq        int
	Kind       string
	Subject    string
	Detail     map[string]any
	ErrorClass string
	DurationMS int
	TokensIn   int
	TokensOut  int
	At         time.Time
}

// Trace event kinds.
const (
	EventModelCall   = "model_call"
	EventModelError  = "model_error"
	EventModelRetry  = "model_retry"
	EventToolCall    = "tool_call"
	EventToolResult  = "tool_result"
	EventToolDenied  = "tool_denied"
	EventToolUnknown = "tool_unknown"
	EventLimit       = "limit"
	EventNote        = "note"
)

// AppendEvent writes one trace row.
//
// Append-only, enforced by a trigger: a trace that can be rewritten is not
// evidence. Messages carry the conversation but not what happened *between*
// messages, and a model call that returned unparseable content and was retried
// leaves no message at all — which is exactly the event worth reading when a
// turn behaved oddly.
func (Turns) AppendEvent(ctx context.Context, q store.Querier, e *TurnEvent) error {
	detail, err := json.Marshal(orEmpty(e.Detail))
	if err != nil {
		return err
	}
	const sql = `
		INSERT INTO turn_events (id, turn_id, seq, kind, subject, detail, error_class,
		                         duration_ms, tokens_in, tokens_out, at)
		VALUES ($1, $2,
		        coalesce((SELECT max(seq) FROM turn_events WHERE turn_id = $2), 0) + 1,
		        $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING seq`
	return q.QueryRow(ctx, sql, e.ID, e.TurnID, e.Kind, e.Subject, detail,
		nullIfEmpty(e.ErrorClass), e.DurationMS, e.TokensIn, e.TokensOut, e.At).Scan(&e.Seq)
}

// Events returns a turn's trace in order.
func (Turns) Events(ctx context.Context, q store.Querier, turnID uuid.UUID) ([]TurnEvent, error) {
	const sql = `
		SELECT id, turn_id, seq, kind, subject, detail, coalesce(error_class, ''),
		       coalesce(duration_ms, 0), coalesce(tokens_in, 0), coalesce(tokens_out, 0), at
		  FROM turn_events WHERE turn_id = $1 ORDER BY seq`
	rows, err := q.Query(ctx, sql, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TurnEvent
	for rows.Next() {
		var (
			e      TurnEvent
			detail []byte
		)
		if err := rows.Scan(&e.ID, &e.TurnID, &e.Seq, &e.Kind, &e.Subject, &detail,
			&e.ErrorClass, &e.DurationMS, &e.TokensIn, &e.TokensOut, &e.At); err != nil {
			return nil, err
		}
		if len(detail) > 0 {
			if err := json.Unmarshal(detail, &e.Detail); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
