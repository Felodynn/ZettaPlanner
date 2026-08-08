package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Checklists is the checklist_items aggregate.
type Checklists struct{}

const checklistColumns = `id, work_item_id, text, checked, checked_by, checked_at, seq, deleted_at`

// Create inserts a checklist item.
func (Checklists) Create(ctx context.Context, q store.Querier, c *plan.ChecklistItem) error {
	const sql = `
		INSERT INTO checklist_items (id, work_item_id, text, checked, checked_by, checked_at, seq)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := q.Exec(ctx, sql, c.ID, c.WorkItemID, c.Text, c.Checked, c.CheckedBy, c.CheckedAt, c.Seq)
	return err
}

// ByID returns one live checklist item.
func (Checklists) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*plan.ChecklistItem, error) {
	const sql = `SELECT ` + checklistColumns + ` FROM checklist_items WHERE id = $1 AND deleted_at IS NULL`
	return scanChecklistItem(q.QueryRow(ctx, sql, id))
}

// ListByItem returns a work item's live checklist in order.
func (Checklists) ListByItem(ctx context.Context, q store.Querier, itemID uuid.UUID) ([]*plan.ChecklistItem, error) {
	const sql = `
		SELECT ` + checklistColumns + `
		  FROM checklist_items
		 WHERE work_item_id = $1 AND deleted_at IS NULL
		 ORDER BY seq, id`
	rows, err := q.Query(ctx, sql, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*plan.ChecklistItem
	for rows.Next() {
		c, err := scanChecklistItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NextSeq returns the position a new checklist item should take.
func (Checklists) NextSeq(ctx context.Context, q store.Querier, itemID uuid.UUID) (int, error) {
	const sql = `SELECT coalesce(max(seq), 0) + 1 FROM checklist_items WHERE work_item_id = $1`
	var seq int
	err := q.QueryRow(ctx, sql, itemID).Scan(&seq)
	return seq, err
}

// Update writes the whole row back.
//
// checked_by and checked_at are carried in the struct rather than set here from
// now(), because an un-tick keeps the pair as it was: "who ticked this, and
// when" survives somebody clearing the box. The clear itself is a transition in
// the audit log, which is where the un-tick is actually recorded.
func (Checklists) Update(ctx context.Context, q store.Querier, c *plan.ChecklistItem) error {
	const sql = `
		UPDATE checklist_items
		   SET text = $2, checked = $3, checked_by = $4, checked_at = $5, seq = $6
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, c.ID, c.Text, c.Checked, c.CheckedBy, c.CheckedAt, c.Seq)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("checklist item")
	}
	return nil
}

// Delete soft-deletes a checklist item.
func (Checklists) Delete(ctx context.Context, q store.Querier, id uuid.UUID) error {
	const sql = `UPDATE checklist_items SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("checklist item")
	}
	return nil
}

func scanChecklistItem(row scanner) (*plan.ChecklistItem, error) {
	var c plan.ChecklistItem
	err := row.Scan(&c.ID, &c.WorkItemID, &c.Text, &c.Checked, &c.CheckedBy, &c.CheckedAt,
		&c.Seq, &c.DeletedAt)
	if err != nil {
		return nil, notFound(err, "checklist item")
	}
	return &c, nil
}
