package repo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Dependencies is the item_dependencies aggregate: directed edges between work
// items (CONTRACT §3.5). Not a tree — an item may block many and be blocked by
// many.
type Dependencies struct{}

// MaxDependencyDepth bounds the cycle walk.
//
// Generous rather than tight. Cycles are rejected on every write, so no cycle
// can pre-exist and the walk only ever traverses an acyclic graph plus the one
// edge being added — but the length of that chain is a property of somebody's
// plan, not of this code, and a limit small enough to be hit is a limit that
// lets a cycle through. The bound exists so that a bug in the recursion
// terminates, not to express an opinion about how deep a plan may be. Hitting
// it is reported, never ignored.
const MaxDependencyDepth = 512

// Set replaces every edge of one kind leaving an item.
//
// Replace rather than merge: the endpoint is a PUT, and "these are the items I
// block" is a complete statement. A merge would make removing a dependency
// impossible without a second verb.
func (Dependencies) Set(ctx context.Context, q store.Querier, fromItemID uuid.UUID,
	kind plan.DependencyKind, targets []uuid.UUID, actorID uuid.UUID, now time.Time) error {

	// item_dependencies is a link table with no lifecycle of its own. It
	// carries no deleted_at, nothing in audit_log points at a dependency row,
	// and the edit itself is recorded as item_dependencies.set with the before
	// and after sets. Keeping tombstones here would mean every traversal
	// filtering them, for no history that is not already recorded.
	const del = `DELETE FROM item_dependencies WHERE from_item_id = $1 AND kind = $2` // zp:hard-delete-ok
	if _, err := q.Exec(ctx, del, fromItemID, string(kind)); err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	const ins = `
		INSERT INTO item_dependencies (from_item_id, to_item_id, kind, actor_id, created_at)
		SELECT $1, t, $2, $3, $4 FROM unnest($5::uuid[]) AS t
		ON CONFLICT DO NOTHING`
	_, err := q.Exec(ctx, ins, fromItemID, string(kind), actorID, now, targets)
	return err
}

const dependencyColumns = `d.from_item_id, d.to_item_id, d.kind, d.actor_id, d.created_at,
                           f.ref, f.title, t.ref, t.title`

// Outgoing returns the edges leaving an item — what it blocks, what it relates
// to — with both ends named.
func (Dependencies) Outgoing(ctx context.Context, q store.Querier, itemID uuid.UUID) ([]plan.Dependency, error) {
	const sql = `
		SELECT ` + dependencyColumns + `
		  FROM item_dependencies d
		  JOIN work_items f ON f.id = d.from_item_id
		  JOIN work_items t ON t.id = d.to_item_id
		 WHERE d.from_item_id = $1 AND t.deleted_at IS NULL
		 ORDER BY d.kind, t.ref`
	return queryDependencies(ctx, q, sql, itemID)
}

// Incoming returns the edges arriving at an item — what blocks it.
func (Dependencies) Incoming(ctx context.Context, q store.Querier, itemID uuid.UUID) ([]plan.Dependency, error) {
	const sql = `
		SELECT ` + dependencyColumns + `
		  FROM item_dependencies d
		  JOIN work_items f ON f.id = d.from_item_id
		  JOIN work_items t ON t.id = d.to_item_id
		 WHERE d.to_item_id = $1 AND f.deleted_at IS NULL
		 ORDER BY d.kind, f.ref`
	return queryDependencies(ctx, q, sql, itemID)
}

func queryDependencies(ctx context.Context, q store.Querier, sql string, itemID uuid.UUID) ([]plan.Dependency, error) {
	rows, err := q.Query(ctx, sql, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []plan.Dependency
	for rows.Next() {
		var (
			d    plan.Dependency
			kind string
		)
		if err := rows.Scan(&d.FromItemID, &d.ToItemID, &kind, &d.ActorID, &d.CreatedAt,
			&d.FromRef, &d.FromTitle, &d.ToRef, &d.ToTitle); err != nil {
			return nil, err
		}
		d.Kind = plan.DependencyKind(kind)
		out = append(out, d)
	}
	return out, rows.Err()
}

// FindCycle looks for a loop in `blocks` reachable from one item.
//
// Seeded from that item's outgoing edges rather than from the whole project.
// Cycles are rejected on every write, so the graph was acyclic before this
// transaction — which means any cycle now present must run through one of the
// edges just written, and a walk from them finds it.
//
// Called **inside the writing transaction, after the write**. Checking before
// would have to model the proposed state in Go; checking after lets Postgres
// answer against the actual rows and the rollback undo it.
//
// Returns the loop as a path of ids, first repeated last. truncated reports
// that the walk hit MaxDependencyDepth, which means the answer is "do not
// know" rather than "no cycle" — the caller must refuse rather than proceed.
func (Dependencies) FindCycle(ctx context.Context, q store.Querier, fromItemID uuid.UUID) (path []uuid.UUID, truncated bool, err error) {
	const sql = `
		WITH RECURSIVE walk(node_id, path, depth, looped) AS (
		    SELECT d.to_item_id,
		           ARRAY[d.from_item_id, d.to_item_id],
		           1,
		           d.to_item_id = d.from_item_id
		      FROM item_dependencies d
		     WHERE d.from_item_id = $1 AND d.kind = 'blocks'
		  UNION ALL
		    SELECT d.to_item_id,
		           w.path || d.to_item_id,
		           w.depth + 1,
		           d.to_item_id = ANY(w.path)
		      FROM walk w
		      JOIN item_dependencies d ON d.from_item_id = w.node_id AND d.kind = 'blocks'
		     WHERE NOT w.looped AND w.depth < $2
		)
		SELECT (SELECT path FROM walk WHERE looped ORDER BY depth LIMIT 1),
		       coalesce((SELECT bool_or(depth >= $2) FROM walk), false)`

	var found []uuid.UUID
	if err := q.QueryRow(ctx, sql, fromItemID, MaxDependencyDepth).Scan(&found, &truncated); err != nil {
		return nil, false, err
	}
	return found, truncated, nil
}
