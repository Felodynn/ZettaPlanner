package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// SubTasks is the subtasks aggregate. A subtask is a leaf: there is no deeper
// nesting, so there is no recursive query anywhere in this file.
type SubTasks struct{}

const subTaskColumns = `id, work_item_id, title, status, seq, assignee_id,
                        provenance, actor_id, created_at, updated_at, deleted_at`

// Create inserts a subtask.
func (SubTasks) Create(ctx context.Context, q store.Querier, s *plan.SubTask) error {
	const sql = `
		INSERT INTO sub_tasks
		  (id, work_item_id, title, status, seq, assignee_id, provenance, actor_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`
	_, err := q.Exec(ctx, sql, s.ID, s.WorkItemID, s.Title, string(s.Status), s.Seq,
		s.AssigneeID, string(s.Provenance), s.ActorID, s.CreatedAt)
	return err
}

// ByID returns one live subtask.
func (SubTasks) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*plan.SubTask, error) {
	const sql = `SELECT ` + subTaskColumns + ` FROM sub_tasks WHERE id = $1 AND deleted_at IS NULL`
	return scanSubTask(q.QueryRow(ctx, sql, id))
}

// ListByItem returns a work item's live subtasks in order.
func (SubTasks) ListByItem(ctx context.Context, q store.Querier, itemID uuid.UUID) ([]*plan.SubTask, error) {
	const sql = `
		SELECT ` + subTaskColumns + `
		  FROM sub_tasks
		 WHERE work_item_id = $1 AND deleted_at IS NULL
		 ORDER BY seq, created_at`
	rows, err := q.Query(ctx, sql, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*plan.SubTask
	for rows.Next() {
		s, err := scanSubTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// NextSeq returns the position a new subtask should take.
func (SubTasks) NextSeq(ctx context.Context, q store.Querier, itemID uuid.UUID) (int, error) {
	const sql = `SELECT coalesce(max(seq), 0) + 1 FROM sub_tasks WHERE work_item_id = $1`
	var seq int
	err := q.QueryRow(ctx, sql, itemID).Scan(&seq)
	return seq, err
}

// Update writes the whole row back.
func (SubTasks) Update(ctx context.Context, q store.Querier, s *plan.SubTask) error {
	const sql = `
		UPDATE sub_tasks
		   SET title = $2, status = $3, seq = $4, assignee_id = $5,
		       provenance = $6, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, s.ID, s.Title, string(s.Status), s.Seq, s.AssigneeID,
		string(s.Provenance))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("subtask")
	}
	return nil
}

// Delete soft-deletes a subtask.
func (SubTasks) Delete(ctx context.Context, q store.Querier, id uuid.UUID) error {
	const sql = `UPDATE sub_tasks SET deleted_at = now(), updated_at = now()
	              WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("subtask")
	}
	return nil
}

func scanSubTask(row scanner) (*plan.SubTask, error) {
	var (
		s          plan.SubTask
		status     string
		provenance string
	)
	err := row.Scan(&s.ID, &s.WorkItemID, &s.Title, &status, &s.Seq, &s.AssigneeID,
		&provenance, &s.ActorID, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt)
	if err != nil {
		return nil, notFound(err, "subtask")
	}
	s.Status = plan.Status(status)
	s.Provenance = domain.Provenance(provenance)
	return &s, nil
}
