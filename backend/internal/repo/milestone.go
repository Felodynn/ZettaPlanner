package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Milestones is the milestones aggregate.
type Milestones struct{}

const milestoneColumns = `id, project_id, name, intent, seq, start_date, due_date, status,
                          provenance, actor_id, created_at, updated_at, deleted_at`

// Create inserts a milestone.
func (Milestones) Create(ctx context.Context, q store.Querier, m *plan.Milestone) error {
	const sql = `
		INSERT INTO milestones
		  (id, project_id, name, intent, seq, start_date, due_date, status,
		   provenance, actor_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)`
	_, err := q.Exec(ctx, sql,
		m.ID, m.ProjectID, m.Name, nullIfEmpty(m.Intent), m.Seq, m.StartDate, m.DueDate,
		string(m.Status), string(m.Provenance), m.ActorID, m.CreatedAt)
	return err
}

// ByID returns one live milestone.
func (Milestones) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*plan.Milestone, error) {
	const sql = `SELECT ` + milestoneColumns + ` FROM milestones WHERE id = $1 AND deleted_at IS NULL`
	return scanMilestone(q.QueryRow(ctx, sql, id))
}

// ListByProject returns a project's live milestones in plan order.
func (Milestones) ListByProject(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]*plan.Milestone, error) {
	const sql = `
		SELECT ` + milestoneColumns + `
		  FROM milestones
		 WHERE project_id = $1 AND deleted_at IS NULL
		 ORDER BY seq, created_at`
	rows, err := q.Query(ctx, sql, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*plan.Milestone
	for rows.Next() {
		m, err := scanMilestone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// NextSeq returns the position a new milestone should take: one past the last.
//
// Deleted milestones are counted. A seq is a position in a plan people talk
// about ("the third milestone"), and reusing the slot of a dropped one makes
// two different milestones the third.
func (Milestones) NextSeq(ctx context.Context, q store.Querier, projectID uuid.UUID) (int, error) {
	const sql = `SELECT coalesce(max(seq), 0) + 1 FROM milestones WHERE project_id = $1`
	var seq int
	err := q.QueryRow(ctx, sql, projectID).Scan(&seq)
	return seq, err
}

// Update writes the whole row back. The service has already applied the patch
// and checked every field against the ownership table.
func (Milestones) Update(ctx context.Context, q store.Querier, m *plan.Milestone) error {
	const sql = `
		UPDATE milestones
		   SET name = $2, intent = $3, seq = $4, start_date = $5, due_date = $6,
		       status = $7, provenance = $8, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, m.ID, m.Name, nullIfEmpty(m.Intent), m.Seq,
		m.StartDate, m.DueDate, string(m.Status), string(m.Provenance))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("milestone")
	}
	return nil
}

// Delete soft-deletes a milestone.
func (Milestones) Delete(ctx context.Context, q store.Querier, id uuid.UUID) error {
	const sql = `UPDATE milestones SET deleted_at = now(), updated_at = now()
	              WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("milestone")
	}
	return nil
}

// DetachItems clears the milestone from every work item pointing at it.
//
// Called when a milestone is deleted. The alternative — leaving the pointer —
// gives work items a milestone_id that resolves to nothing, and every join
// downstream has to remember to filter it. The items themselves are untouched:
// deleting a grouping must not delete the work.
func (Milestones) DetachItems(ctx context.Context, q store.Querier, milestoneID uuid.UUID) (int, error) {
	const sql = `UPDATE work_items SET milestone_id = NULL, updated_at = now()
	              WHERE milestone_id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, milestoneID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func scanMilestone(row scanner) (*plan.Milestone, error) {
	var (
		m          plan.Milestone
		intent     *string
		status     string
		provenance string
	)
	err := row.Scan(&m.ID, &m.ProjectID, &m.Name, &intent, &m.Seq, &m.StartDate, &m.DueDate,
		&status, &provenance, &m.ActorID, &m.CreatedAt, &m.UpdatedAt, &m.DeletedAt)
	if err != nil {
		return nil, notFound(err, "milestone")
	}
	if intent != nil {
		m.Intent = *intent
	}
	m.Status = plan.MilestoneStatus(status)
	m.Provenance = domain.Provenance(provenance)
	return &m, nil
}
