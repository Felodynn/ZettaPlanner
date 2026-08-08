package repo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Worklogs is the worklogs aggregate.
//
// There is no Update and no Delete in this file, and that is the design. A
// worklog is corrected by adding another (CONTRACT §6.5 constraint 1) — which
// is what makes the log evidence rather than a current-state field, and what
// makes "what did we believe on the 12th" answerable at all.
type Worklogs struct{}

const worklogColumns = `w.id, w.project_id, w.work_item_id, w.actor_id, w.kind, w.body, w.created_at,
                        coalesce(i.ref, ''), coalesce(u.display_name, coalesce(a.agent_name, ''))`

const worklogFrom = `
	  FROM worklogs w
	  LEFT JOIN work_items i ON i.id = w.work_item_id
	  LEFT JOIN actors a     ON a.id = w.actor_id
	  LEFT JOIN users u      ON u.id = a.user_id`

// Create appends a worklog.
func (Worklogs) Create(ctx context.Context, q store.Querier, w *plan.Worklog) error {
	const sql = `
		INSERT INTO worklogs (id, project_id, work_item_id, actor_id, kind, body, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := q.Exec(ctx, sql, w.ID, w.ProjectID, w.WorkItemID, w.ActorID,
		string(w.Kind), w.Body, w.CreatedAt)
	return err
}

// ByID returns one worklog.
func (Worklogs) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*plan.Worklog, error) {
	const sql = `SELECT ` + worklogColumns + worklogFrom + ` WHERE w.id = $1`
	return scanWorklog(q.QueryRow(ctx, sql, id))
}

// WorklogFilter is the query surface of GET /projects/{pid}/worklogs.
type WorklogFilter struct {
	WorkItemID *uuid.UUID
	Kind       plan.WorklogKind
	Since      *time.Time
	Limit      int
}

// List returns a project's worklogs, newest first.
func (Worklogs) List(ctx context.Context, q store.Querier, projectID uuid.UUID, f WorklogFilter) ([]*plan.Worklog, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	const sql = `
		SELECT ` + worklogColumns + worklogFrom + `
		 WHERE w.project_id = $1
		   AND ($2::uuid IS NULL OR w.work_item_id = $2)
		   AND ($3 = '' OR w.kind = $3)
		   AND ($4::timestamptz IS NULL OR w.created_at >= $4)
		 ORDER BY w.created_at DESC, w.id DESC
		 LIMIT $5`
	rows, err := q.Query(ctx, sql, projectID, f.WorkItemID, string(f.Kind), f.Since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*plan.Worklog
	for rows.Next() {
		w, err := scanWorklog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func scanWorklog(row scanner) (*plan.Worklog, error) {
	var (
		w    plan.Worklog
		kind string
	)
	err := row.Scan(&w.ID, &w.ProjectID, &w.WorkItemID, &w.ActorID, &kind, &w.Body,
		&w.CreatedAt, &w.ItemRef, &w.ActorName)
	if err != nil {
		return nil, notFound(err, "worklog")
	}
	w.Kind = plan.WorklogKind(kind)
	return &w, nil
}
