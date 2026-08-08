package repo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Dashboards answers the questions a project view opens with.
//
// **There is no dashboard table and there will not be one.** Every figure here
// is a `GROUP BY` over live rows, computed on read, exactly like the warnings
// (§3.5) and the digest's harness half (§6.3). A materialised rollup is stale
// between the write and the refresh, reads identically whether it is true now
// or was true in April, and is the sort of thing that goes wrong quietly for a
// week before anybody notices the numbers do not add up.
//
// The cost is real — this is four aggregate queries on every open — and it is
// the right price. A project has thousands of work items, not millions, and
// every index these need is already on the table.
type Dashboards struct{}

// closedStatuses are the statuses that mean the work is no longer coming.
// `dropped` counts: the decision not to do something is as final as doing it,
// and counting dropped items as open makes every backlog figure a lie.
var closedStatuses = []string{string(plan.StatusDone), string(plan.StatusDropped)}

// MilestoneStat is one milestone's rollup.
type MilestoneStat struct {
	MilestoneID uuid.UUID
	Total       int
	Open        int
	Done        int
	Blocked     int
	Overdue     int
}

// MilestoneRollup counts the work under each of a project's milestones.
func (Dashboards) MilestoneRollup(ctx context.Context, q store.Querier, projectID uuid.UUID) (map[uuid.UUID]MilestoneStat, error) {
	const sql = `
		SELECT m.id,
		       count(w.id)                                                        AS total,
		       count(w.id) FILTER (WHERE NOT (w.status = ANY($2)))                AS open,
		       count(w.id) FILTER (WHERE w.status = 'done')                       AS done,
		       count(w.id) FILTER (WHERE w.status = 'blocked')                    AS blocked,
		       count(w.id) FILTER (WHERE NOT (w.status = ANY($2))
		                             AND w.due_date < current_date)               AS overdue
		  FROM milestones m
		  LEFT JOIN work_items w
		         ON w.milestone_id = m.id AND w.deleted_at IS NULL
		 WHERE m.project_id = $1 AND m.deleted_at IS NULL
		 GROUP BY m.id`

	rows, err := q.Query(ctx, sql, projectID, closedStatuses)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID]MilestoneStat{}
	for rows.Next() {
		var s MilestoneStat
		if err := rows.Scan(&s.MilestoneID, &s.Total, &s.Open, &s.Done, &s.Blocked, &s.Overdue); err != nil {
			return nil, err
		}
		out[s.MilestoneID] = s
	}
	return out, rows.Err()
}

// AssigneeStat is one person's load. UserID is nil for the unassigned pile.
type AssigneeStat struct {
	UserID     *uuid.UUID
	Name       string
	Open       int
	InProgress int
	Blocked    int
	Overdue    int
}

// AssigneeLoad counts each person's open work, including the unassigned pile.
//
// Unassigned is a row rather than a separate field: it is the single most
// actionable number on the page, and a shape that puts it somewhere else is a
// shape a frontend will forget to render.
func (Dashboards) AssigneeLoad(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]AssigneeStat, error) {
	const sql = `
		SELECT w.assignee_id, coalesce(u.display_name, ''),
		       count(*)                                                   AS open,
		       count(*) FILTER (WHERE w.status = 'in_progress')           AS in_progress,
		       count(*) FILTER (WHERE w.status = 'blocked')               AS blocked,
		       count(*) FILTER (WHERE w.due_date < current_date)          AS overdue
		  FROM work_items w
		  LEFT JOIN users u ON u.id = w.assignee_id
		 WHERE w.project_id = $1
		   AND w.deleted_at IS NULL
		   AND NOT (w.status = ANY($2))
		 GROUP BY w.assignee_id, u.display_name
		 ORDER BY open DESC, u.display_name NULLS LAST`

	rows, err := q.Query(ctx, sql, projectID, closedStatuses)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AssigneeStat
	for rows.Next() {
		var s AssigneeStat
		if err := rows.Scan(&s.UserID, &s.Name, &s.Open, &s.InProgress, &s.Blocked, &s.Overdue); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DatedItem is one open item with a date worth looking at.
type DatedItem struct {
	ID       uuid.UUID
	Ref      string
	Title    string
	Status   plan.Status
	Priority plan.Priority
	DueDate  *time.Time
	Assignee string
	Overdue  bool
}

// Upcoming returns the open items due within `withinDays`, and every open item
// already overdue.
//
// One query for both, because they are one question — what needs attention this
// week — and splitting them into two endpoints makes the frontend decide where
// the boundary is.
func (Dashboards) Upcoming(ctx context.Context, q store.Querier, projectID uuid.UUID, withinDays int) ([]DatedItem, error) {
	const sql = `
		SELECT w.id, w.ref, w.title, w.status, coalesce(w.priority, ''), w.due_date,
		       coalesce(u.display_name, ''), (w.due_date < current_date) AS overdue
		  FROM work_items w
		  LEFT JOIN users u ON u.id = w.assignee_id
		 WHERE w.project_id = $1
		   AND w.deleted_at IS NULL
		   AND NOT (w.status = ANY($2))
		   AND w.due_date IS NOT NULL
		   AND w.due_date <= current_date + make_interval(days => $3)
		 ORDER BY w.due_date, w.ref`

	rows, err := q.Query(ctx, sql, projectID, closedStatuses, withinDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DatedItem
	for rows.Next() {
		var (
			it       DatedItem
			status   string
			priority string
		)
		if err := rows.Scan(&it.ID, &it.Ref, &it.Title, &status, &priority, &it.DueDate,
			&it.Assignee, &it.Overdue); err != nil {
			return nil, err
		}
		it.Status, it.Priority = plan.Status(status), plan.Priority(priority)
		out = append(out, it)
	}
	return out, rows.Err()
}

// BlockedItem is an item in `blocked`, with what is actually holding it.
type BlockedItem struct {
	ID    uuid.UUID
	Ref   string
	Title string
	// BlockedBy names the unfinished blockers. Empty is a real and interesting
	// answer: somebody set the status to blocked and never recorded what by, so
	// nothing can tell them when it clears.
	BlockedBy []string
}

// Blocked returns every item whose status is `blocked`, with its live blockers.
func (Dashboards) Blocked(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]BlockedItem, error) {
	const sql = `
		SELECT w.id, w.ref, w.title,
		       coalesce(array_agg(b.ref ORDER BY b.ref) FILTER (WHERE b.id IS NOT NULL), '{}')
		  FROM work_items w
		  LEFT JOIN item_dependencies d ON d.to_item_id = w.id AND d.kind = 'blocks'
		  LEFT JOIN work_items b
		         ON b.id = d.from_item_id
		        AND b.deleted_at IS NULL
		        AND NOT (b.status = ANY($2))
		 WHERE w.project_id = $1
		   AND w.deleted_at IS NULL
		   AND w.status = 'blocked'
		 GROUP BY w.id, w.ref, w.title
		 ORDER BY w.ref`

	rows, err := q.Query(ctx, sql, projectID, closedStatuses)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BlockedItem
	for rows.Next() {
		var it BlockedItem
		if err := rows.Scan(&it.ID, &it.Ref, &it.Title, &it.BlockedBy); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Totals is the project's headline counts.
type Totals struct {
	Items          int
	Open           int
	ByStatus       map[string]int
	Untriaged      int
	OpenDeviations int
	BySeverity     map[string]int
	PendingReview  int
}

// Counts returns the headline figures in two queries.
func (Dashboards) Counts(ctx context.Context, q store.Querier, projectID uuid.UUID) (Totals, error) {
	out := Totals{ByStatus: map[string]int{}, BySeverity: map[string]int{}}

	const items = `
		SELECT status, count(*), count(*) FILTER (WHERE priority IS NULL)
		  FROM work_items
		 WHERE project_id = $1 AND deleted_at IS NULL
		 GROUP BY status`
	rows, err := q.Query(ctx, items, projectID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var (
			status            string
			count, noPriority int
		)
		if err := rows.Scan(&status, &count, &noPriority); err != nil {
			rows.Close()
			return out, err
		}
		out.ByStatus[status] = count
		out.Items += count
		if status != string(plan.StatusDone) && status != string(plan.StatusDropped) {
			out.Open += count
			// An unset priority is not `normal` (§3.5a): an untriaged item and
			// one explicitly judged ordinary are different facts, and only the
			// first is a thing somebody has to do.
			out.Untriaged += noPriority
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	const rest = `
		SELECT severity, count(*)
		  FROM deviations
		 WHERE project_id = $1 AND deleted_at IS NULL AND resolved_at IS NULL
		 GROUP BY severity`
	devRows, err := q.Query(ctx, rest, projectID)
	if err != nil {
		return out, err
	}
	for devRows.Next() {
		var (
			severity string
			count    int
		)
		if err := devRows.Scan(&severity, &count); err != nil {
			devRows.Close()
			return out, err
		}
		out.BySeverity[severity] = count
		out.OpenDeviations += count
	}
	devRows.Close()
	if err := devRows.Err(); err != nil {
		return out, err
	}

	const pending = `SELECT count(*) FROM proposals WHERE project_id = $1 AND status = 'pending'`
	if err := q.QueryRow(ctx, pending, projectID).Scan(&out.PendingReview); err != nil {
		return out, err
	}
	return out, nil
}
