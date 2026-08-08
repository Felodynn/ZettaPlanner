package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Warnings computes date violations. There is no table behind this file.
//
// A stored warning is stale the moment a date moves, and the staleness is
// invisible — the row reads identically whether it is true now or was true in
// April. Two queries against live rows cost less than one reconciliation job
// that is wrong between runs.
//
// Both queries select only the columns the message needs. The structs they
// return are therefore partial: id, ref, title, and the two dates in question.
// They are never written back and never handed to anything that reads more
// than that.
type Warnings struct{}

// ForProject returns every violation in a project, ordered worst-overrun first.
func (w Warnings) ForProject(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]plan.Warning, error) {
	blocker, err := w.blockerDates(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	milestone, err := w.milestoneDates(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	out := append(blocker, milestone...)
	plan.SortWarnings(out)
	return out, nil
}

// blockerDates finds items scheduled to start before the item blocking them is
// due to finish.
//
// A blocker that is already `done` is not a violation regardless of its dates:
// the thing the item was waiting for has happened. Same for an item that is
// itself finished — its dates are history, and a warning about history is
// noise that trains people to ignore the list.
func (Warnings) blockerDates(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]plan.Warning, error) {
	const sql = `
		SELECT i.id, i.ref, i.title, i.start_date,
		       b.id, b.ref, b.title, b.due_date
		  FROM item_dependencies d
		  JOIN work_items b ON b.id = d.from_item_id AND b.deleted_at IS NULL
		  JOIN work_items i ON i.id = d.to_item_id   AND i.deleted_at IS NULL
		 WHERE d.kind = 'blocks'
		   AND i.project_id = $1
		   AND i.start_date IS NOT NULL
		   AND b.due_date IS NOT NULL
		   AND i.start_date < b.due_date
		   AND i.status <> ALL($2::text[])
		   AND b.status <> ALL($2::text[])
		 ORDER BY i.ref, b.ref`
	rows, err := q.Query(ctx, sql, projectID, plan.ClosedStatuses())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []plan.Warning
	for rows.Next() {
		var item, blocker plan.WorkItem
		if err := rows.Scan(&item.ID, &item.Ref, &item.Title, &item.StartDate,
			&blocker.ID, &blocker.Ref, &blocker.Title, &blocker.DueDate); err != nil {
			return nil, err
		}
		out = append(out, plan.BlockerDateWarning(&item, &blocker))
	}
	return out, rows.Err()
}

// milestoneDates finds items due after the milestone they belong to closes.
func (Warnings) milestoneDates(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]plan.Warning, error) {
	const sql = `
		SELECT i.id, i.ref, i.title, i.due_date,
		       m.id, m.name, m.due_date
		  FROM work_items i
		  JOIN milestones m ON m.id = i.milestone_id AND m.deleted_at IS NULL
		 WHERE i.project_id = $1
		   AND i.deleted_at IS NULL
		   AND i.due_date IS NOT NULL
		   AND m.due_date IS NOT NULL
		   AND i.due_date > m.due_date
		   AND i.status <> ALL($2::text[])
		   AND m.status <> ALL($3::text[])
		 ORDER BY i.ref`
	rows, err := q.Query(ctx, sql, projectID, plan.ClosedStatuses(), plan.ClosedMilestoneStatuses())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []plan.Warning
	for rows.Next() {
		var (
			item plan.WorkItem
			m    plan.Milestone
		)
		if err := rows.Scan(&item.ID, &item.Ref, &item.Title, &item.DueDate,
			&m.ID, &m.Name, &m.DueDate); err != nil {
			return nil, err
		}
		out = append(out, plan.MilestoneDateWarning(&item, &m))
	}
	return out, rows.Err()
}
