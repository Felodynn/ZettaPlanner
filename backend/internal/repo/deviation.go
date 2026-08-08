package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Deviations is the deviations aggregate: recorded divergences between the plan
// and what actually happened.
type Deviations struct{}

const deviationColumns = `d.id, d.project_id, d.work_item_id, d.worklog_id, d.summary, d.severity,
                          d.resolution, d.resolved_at, d.actor_id, d.created_at, d.deleted_at,
                          coalesce(i.ref, '')`

const deviationFrom = `
	  FROM deviations d
	  LEFT JOIN work_items i ON i.id = d.work_item_id`

// Create inserts a deviation.
func (Deviations) Create(ctx context.Context, q store.Querier, d *plan.Deviation) error {
	const sql = `
		INSERT INTO deviations
		  (id, project_id, work_item_id, worklog_id, summary, severity, resolution,
		   resolved_at, actor_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err := q.Exec(ctx, sql, d.ID, d.ProjectID, d.WorkItemID, d.WorklogID, d.Summary,
		string(d.Severity), nullIfEmpty(d.Resolution), d.ResolvedAt, d.ActorID, d.CreatedAt)
	return err
}

// ByID returns one live deviation.
func (Deviations) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*plan.Deviation, error) {
	const sql = `SELECT ` + deviationColumns + deviationFrom + ` WHERE d.id = $1 AND d.deleted_at IS NULL`
	return scanDeviation(q.QueryRow(ctx, sql, id))
}

// DeviationFilter is the query surface of GET /projects/{pid}/deviations.
type DeviationFilter struct {
	WorkItemID *uuid.UUID
	// OpenOnly restricts to unresolved deviations, which is what the digest and
	// the skeleton context actually want.
	OpenOnly bool
}

// ListByProject returns a project's live deviations, newest first.
func (Deviations) ListByProject(ctx context.Context, q store.Querier, projectID uuid.UUID, f DeviationFilter) ([]*plan.Deviation, error) {
	const sql = `
		SELECT ` + deviationColumns + deviationFrom + `
		 WHERE d.project_id = $1
		   AND d.deleted_at IS NULL
		   AND ($2::uuid IS NULL OR d.work_item_id = $2)
		   AND (NOT $3::bool OR d.resolved_at IS NULL)
		 ORDER BY d.created_at DESC`
	rows, err := q.Query(ctx, sql, projectID, f.WorkItemID, f.OpenOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*plan.Deviation
	for rows.Next() {
		d, err := scanDeviation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Update writes the whole row back.
func (Deviations) Update(ctx context.Context, q store.Querier, d *plan.Deviation) error {
	const sql = `
		UPDATE deviations
		   SET summary = $2, severity = $3, resolution = $4, resolved_at = $5, work_item_id = $6
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, d.ID, d.Summary, string(d.Severity),
		nullIfEmpty(d.Resolution), d.ResolvedAt, d.WorkItemID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("deviation")
	}
	return nil
}

// Delete soft-deletes a deviation.
func (Deviations) Delete(ctx context.Context, q store.Querier, id uuid.UUID) error {
	const sql = `UPDATE deviations SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("deviation")
	}
	return nil
}

func scanDeviation(row scanner) (*plan.Deviation, error) {
	var (
		d          plan.Deviation
		severity   string
		resolution *string
	)
	err := row.Scan(&d.ID, &d.ProjectID, &d.WorkItemID, &d.WorklogID, &d.Summary, &severity,
		&resolution, &d.ResolvedAt, &d.ActorID, &d.CreatedAt, &d.DeletedAt, &d.ItemRef)
	if err != nil {
		return nil, notFound(err, "deviation")
	}
	d.Severity = plan.Severity(severity)
	if resolution != nil {
		d.Resolution = *resolution
	}
	return &d, nil
}
