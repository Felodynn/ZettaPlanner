package repo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Proposals is the governance aggregate.
//
// Only the reads context assembly needs exist here in Phase 5. The write side
// arrives in Phase 7 — but the reads have to exist now, because CONTRACT §5.2
// makes rejected proposals with their rationales part of the skeleton, and a
// skeleton with a slot that is wired up later is a slot somebody forgets to
// wire up. Until Phase 7 fills the table these return nothing, which is the
// correct answer for a project that has decided nothing yet.
type Proposals struct{}

// DecisionRow is a decided proposal as the skeleton needs it.
type DecisionRow struct {
	ID        uuid.UUID
	Title     string
	Rationale string
	// DecisionRationale is why it was accepted or refused. **Required on a
	// rejection** (§5.2): it is the field whose loss makes an agent re-propose
	// what was already refused.
	DecisionRationale string
	Status            string
	TargetKind        string
	DecidedBy         string
	DecidedAt         time.Time
}

// Decided returns a project's decided proposals of one status, most recent
// first.
func (Proposals) Decided(ctx context.Context, q store.Querier, projectID uuid.UUID, status string, limit int) ([]DecisionRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	const sql = `
		SELECT p.id, p.title, p.rationale, coalesce(p.decision_rationale, ''),
		       p.status, p.target_kind, coalesce(u.display_name, ''), coalesce(p.decided_at, p.created_at)
		  FROM proposals p
		  LEFT JOIN users u ON u.id = p.decided_by
		 WHERE p.project_id = $1 AND p.status = $2
		 ORDER BY coalesce(p.decided_at, p.created_at) DESC, p.id DESC
		 LIMIT $3`
	rows, err := q.Query(ctx, sql, projectID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DecisionRow
	for rows.Next() {
		var d DecisionRow
		if err := rows.Scan(&d.ID, &d.Title, &d.Rationale, &d.DecisionRationale,
			&d.Status, &d.TargetKind, &d.DecidedBy, &d.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
