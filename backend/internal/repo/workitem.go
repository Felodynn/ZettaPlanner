package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// WorkItems is the work items aggregate.
type WorkItems struct{}

const workItemColumns = `id, project_id, milestone_id, ref, title, intent, acceptance_criteria,
                         status, progress_pct, priority, assignee_id, start_date, due_date,
                         provenance, actor_id, created_at, updated_at, deleted_at`

// Create inserts a work item.
func (WorkItems) Create(ctx context.Context, q store.Querier, w *plan.WorkItem) error {
	const sql = `
		INSERT INTO work_items
		  (id, project_id, milestone_id, ref, title, intent, acceptance_criteria,
		   status, progress_pct, priority, assignee_id, start_date, due_date,
		   provenance, actor_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $16)`
	_, err := q.Exec(ctx, sql,
		w.ID, w.ProjectID, w.MilestoneID, w.Ref, w.Title, nullIfEmpty(w.Intent),
		nullIfEmpty(w.Acceptance), string(w.Status), w.ProgressPct,
		nullIfEmpty(string(w.Priority)), w.AssigneeID, w.StartDate, w.DueDate,
		string(w.Provenance), w.ActorID, w.CreatedAt)
	return conflict(err, "a work item with that ref already exists in this project")
}

// ByID returns one live work item.
func (WorkItems) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*plan.WorkItem, error) {
	const sql = `SELECT ` + workItemColumns + ` FROM work_items WHERE id = $1 AND deleted_at IS NULL`
	return scanWorkItem(q.QueryRow(ctx, sql, id))
}

// ByIDAny returns a work item whether or not it has been deleted.
//
// Deletion is soft, and a deleted item's history has to stay reachable (§6.5
// constraint 1): a worklog, an audit entry or a conversation naming ZP-14 leads
// somewhere, and "404" is not an answer that lets anybody read the history that
// is demonstrably still in the database. Callers that must not act on a deleted
// item check DeletedAt themselves — which is a decision, not an oversight.
func (WorkItems) ByIDAny(ctx context.Context, q store.Querier, id uuid.UUID) (*plan.WorkItem, error) {
	const sql = `SELECT ` + workItemColumns + ` FROM work_items WHERE id = $1`
	return scanWorkItem(q.QueryRow(ctx, sql, id))
}

// ByRef resolves a human-facing ref within a project.
func (WorkItems) ByRef(ctx context.Context, q store.Querier, projectID uuid.UUID, ref string) (*plan.WorkItem, error) {
	const sql = `SELECT ` + workItemColumns + ` FROM work_items WHERE project_id = $1 AND ref = $2`
	return scanWorkItem(q.QueryRow(ctx, sql, projectID, ref))
}

// WorkItemFilter is the query surface of GET /projects/{pid}/work-items.
//
// A nil pointer means "do not filter on this". The distinction matters for
// MilestoneID, where "any milestone" and "no milestone at all" are different
// questions and a single nullable field cannot ask both.
type WorkItemFilter struct {
	AssigneeID *uuid.UUID
	Status     plan.Status
	// MilestoneID filters to one milestone. Unassigned selects the items that
	// belong to none; the two are mutually exclusive and Unassigned wins.
	MilestoneID *uuid.UUID
	Unassigned  bool
}

// List returns a project's live work items, filtered.
func (WorkItems) List(ctx context.Context, q store.Querier, projectID uuid.UUID, f WorkItemFilter) ([]*plan.WorkItem, error) {
	const sql = `
		SELECT ` + workItemColumns + `
		  FROM work_items
		 WHERE project_id = $1
		   AND deleted_at IS NULL
		   AND ($2::uuid IS NULL OR assignee_id = $2)
		   AND ($3 = '' OR status = $3)
		   AND ($4::uuid IS NULL OR milestone_id = $4)
		   AND (NOT $5::bool OR milestone_id IS NULL)
		 ORDER BY created_at`
	rows, err := q.Query(ctx, sql, projectID, f.AssigneeID, string(f.Status), f.MilestoneID, f.Unassigned)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*plan.WorkItem
	for rows.Next() {
		w, err := scanWorkItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Update writes the whole row back.
func (WorkItems) Update(ctx context.Context, q store.Querier, w *plan.WorkItem) error {
	const sql = `
		UPDATE work_items
		   SET milestone_id = $2, title = $3, intent = $4, acceptance_criteria = $5,
		       status = $6, progress_pct = $7, priority = $8, assignee_id = $9,
		       start_date = $10, due_date = $11, provenance = $12, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, w.ID, w.MilestoneID, w.Title, nullIfEmpty(w.Intent),
		nullIfEmpty(w.Acceptance), string(w.Status), w.ProgressPct,
		nullIfEmpty(string(w.Priority)), w.AssigneeID, w.StartDate, w.DueDate,
		string(w.Provenance))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("work item")
	}
	return nil
}

// Delete soft-deletes a work item.
//
// Its subtasks, checklist items, dependencies, entity refs and worklogs are all
// left in place. They are the history, and the ref is never reissued, so
// nothing about them becomes ambiguous — whereas cascading the delete would
// remove the evidence of what the item was when somebody decided to drop it.
func (WorkItems) Delete(ctx context.Context, q store.Querier, id uuid.UUID) error {
	const sql = `UPDATE work_items SET deleted_at = now(), updated_at = now()
	              WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("work item")
	}
	return nil
}

// RefsFor resolves ids to human-facing refs in one round trip. Used wherever a
// message names several items — the cycle path, the warning list.
func (WorkItems) RefsFor(ctx context.Context, q store.Querier, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	const sql = `SELECT id, ref FROM work_items WHERE id = ANY($1)`
	rows, err := q.Query(ctx, sql, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[uuid.UUID]string, len(ids))
	for rows.Next() {
		var (
			id  uuid.UUID
			ref string
		)
		if err := rows.Scan(&id, &ref); err != nil {
			return nil, err
		}
		out[id] = ref
	}
	return out, rows.Err()
}

func scanWorkItem(row scanner) (*plan.WorkItem, error) {
	var (
		w          plan.WorkItem
		intent     *string
		acceptance *string
		priority   *string
		status     string
		provenance string
	)
	err := row.Scan(&w.ID, &w.ProjectID, &w.MilestoneID, &w.Ref, &w.Title, &intent, &acceptance,
		&status, &w.ProgressPct, &priority, &w.AssigneeID, &w.StartDate, &w.DueDate,
		&provenance, &w.ActorID, &w.CreatedAt, &w.UpdatedAt, &w.DeletedAt)
	if err != nil {
		return nil, notFound(err, "work item")
	}
	if intent != nil {
		w.Intent = *intent
	}
	if acceptance != nil {
		w.Acceptance = *acceptance
	}
	if priority != nil {
		w.Priority = plan.Priority(*priority)
	}
	w.Status = plan.Status(status)
	w.Provenance = domain.Provenance(provenance)
	return &w, nil
}
