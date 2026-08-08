package repo

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	th "github.com/Felodynn/ZettaPlanner/backend/internal/domain/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Threads is the conversation aggregate.
//
// There is no delete in this file and there will not be one. Chat is transcript
// (D8) and a transcript with holes is worse than none: the holes are exactly
// where somebody decided something they later regretted.
type Threads struct{}

const threadColumns = `t.id, t.project_id, t.scope_kind, t.scope_id, t.created_at`

// Ensure returns the thread for a scope, creating it if it does not exist.
//
// Idempotent by the unique index rather than by a read-then-write: two people
// opening an item at the same moment would otherwise create two threads for it,
// and the second one is invisible until somebody wonders where their message
// went.
func (Threads) Ensure(ctx context.Context, q store.Querier, t *th.Thread) (*th.Thread, error) {
	const sql = `
		INSERT INTO threads (id, project_id, scope_kind, scope_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (project_id, scope_kind, scope_id) DO UPDATE SET project_id = EXCLUDED.project_id
		RETURNING id, project_id, scope_kind, scope_id, created_at`
	var (
		out   th.Thread
		scope string
	)
	err := q.QueryRow(ctx, sql, t.ID, t.ProjectID, string(t.ScopeKind), t.ScopeID, t.CreatedAt).
		Scan(&out.ID, &out.ProjectID, &scope, &out.ScopeID, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	out.ScopeKind = th.ScopeKind(scope)
	return &out, nil
}

// ByID returns one thread.
func (Threads) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*th.Thread, error) {
	const sql = `SELECT ` + threadColumns + ` FROM threads t WHERE t.id = $1`
	return scanThread(q.QueryRow(ctx, sql, id))
}

// ListByProject returns a project's threads with their message counts and the
// label their scope resolves to.
func (Threads) ListByProject(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]*th.Thread, error) {
	const sql = `
		SELECT ` + threadColumns + `,
		       coalesce(w.ref, d.name, ''),
		       (SELECT count(*) FROM messages m WHERE m.thread_id = t.id),
		       (SELECT max(m.created_at) FROM messages m WHERE m.thread_id = t.id)
		  FROM threads t
		  LEFT JOIN work_items w ON w.id = t.scope_id AND t.scope_kind = 'work_item'
		  LEFT JOIN diagrams   d ON d.id = t.scope_id AND t.scope_kind = 'diagram'
		 WHERE t.project_id = $1
		 ORDER BY t.scope_kind, coalesce(w.ref, d.name, ''), t.created_at`
	rows, err := q.Query(ctx, sql, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*th.Thread
	for rows.Next() {
		var (
			t     th.Thread
			scope string
		)
		if err := rows.Scan(&t.ID, &t.ProjectID, &scope, &t.ScopeID, &t.CreatedAt,
			&t.ScopeLabel, &t.MessageCount, &t.LastMessageAt); err != nil {
			return nil, err
		}
		t.ScopeKind = th.ScopeKind(scope)
		out = append(out, &t)
	}
	return out, rows.Err()
}

func scanThread(row scanner) (*th.Thread, error) {
	var (
		t     th.Thread
		scope string
	)
	if err := row.Scan(&t.ID, &t.ProjectID, &scope, &t.ScopeID, &t.CreatedAt); err != nil {
		return nil, notFound(err, "thread")
	}
	t.ScopeKind = th.ScopeKind(scope)
	return &t, nil
}

// ── messages ─────────────────────────────────────────────────────────────────

// Messages is the transcript aggregate. No update, no delete: a message is
// corrected by posting another.
type Messages struct{}

const messageColumns = `m.id, m.thread_id, m.actor_id, m.role, m.content, m.tool_calls,
                        m.turn_id, m.seq, m.created_at`

// Append writes a message, allocating its sequence number under a lock on the
// thread row.
//
// The lock is what makes `UNIQUE (thread_id, seq)` a guarantee rather than a
// race: two writers computing max(seq)+1 concurrently both get the same answer
// and one of them fails, which during a turn means losing a tool result.
func (Messages) Append(ctx context.Context, q store.Querier, m *th.Message) error {
	const lock = `SELECT 1 FROM threads WHERE id = $1 FOR UPDATE`
	if _, err := q.Exec(ctx, lock, m.ThreadID); err != nil {
		return err
	}

	// NULL rather than an empty object when there are no calls. `tool_calls` is
	// a JSON *array* when present, and coercing absence into `{}` produces a
	// column whose two shapes disagree — which surfaces later as a scan failure
	// on the read path rather than as an error here.
	var calls []byte
	if len(m.ToolCalls) > 0 {
		var err error
		if calls, err = json.Marshal(m.ToolCalls); err != nil {
			return err
		}
	}

	const sql = `
		INSERT INTO messages (id, thread_id, actor_id, role, content, tool_calls, turn_id, seq, created_at)
		VALUES ($1, $2, $3, $4, $5, $6,  $7,
		        coalesce((SELECT max(seq) FROM messages WHERE thread_id = $2), 0) + 1, $8)
		RETURNING seq`
	return q.QueryRow(ctx, sql, m.ID, m.ThreadID, m.ActorID, string(m.Role), m.Content,
		calls, m.TurnID, m.CreatedAt).Scan(&m.Seq)
}

// MessageFilter is the query surface of GET /threads/{id}/messages.
type MessageFilter struct {
	// Before returns the messages immediately preceding a sequence number,
	// which is how a transcript pages backwards.
	Before *int64
	Limit  int
	// TurnID restricts to one turn, which is what the trace reads.
	TurnID *uuid.UUID
}

// List returns a thread's messages, oldest first within the page.
func (Messages) List(ctx context.Context, q store.Querier, threadID uuid.UUID, f MessageFilter) ([]*th.Message, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	const sql = `
		SELECT * FROM (
		  SELECT ` + messageColumns + `, coalesce(u.display_name, coalesce(a.agent_name, ''))
		    FROM messages m
		    LEFT JOIN actors a ON a.id = m.actor_id
		    LEFT JOIN users  u ON u.id = a.user_id
		   WHERE m.thread_id = $1
		     AND ($2::bigint IS NULL OR m.seq < $2)
		     AND ($3::uuid IS NULL OR m.turn_id = $3)
		   ORDER BY m.seq DESC
		   LIMIT $4
		) page ORDER BY seq`
	rows, err := q.Query(ctx, sql, threadID, f.Before, f.TurnID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*th.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ForTurn returns every message a turn produced, oldest first.
func (Messages) ForTurn(ctx context.Context, q store.Querier, turnID uuid.UUID) ([]*th.Message, error) {
	const sql = `
		SELECT ` + messageColumns + `, coalesce(u.display_name, coalesce(a.agent_name, ''))
		  FROM messages m
		  LEFT JOIN actors a ON a.id = m.actor_id
		  LEFT JOIN users  u ON u.id = a.user_id
		 WHERE m.turn_id = $1
		 ORDER BY m.seq`
	rows, err := q.Query(ctx, sql, turnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*th.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanMessage(row scanner) (*th.Message, error) {
	var (
		m     th.Message
		role  string
		calls []byte
	)
	if err := row.Scan(&m.ID, &m.ThreadID, &m.ActorID, &role, &m.Content, &calls,
		&m.TurnID, &m.Seq, &m.CreatedAt, &m.ActorName); err != nil {
		return nil, notFound(err, "message")
	}
	m.Role = th.Role(role)
	// Only an array is a call list. Anything else in the column is a row
	// written before this shape settled, and refusing to render the whole
	// transcript because of one is the wrong trade.
	if len(calls) > 0 && calls[0] == '[' {
		if err := json.Unmarshal(calls, &m.ToolCalls); err != nil {
			return nil, err
		}
	}
	return &m, nil
}
