package plan

import (
	"context"

	"github.com/google/uuid"

	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
)

// DefaultHorizonDays is how far ahead the dashboard looks for due dates.
//
// Two weeks: far enough that a due date arrives on the page before the week it
// is due, near enough that the list is still short enough to read. Overdue
// items are always included regardless of how long ago they slipped.
const DefaultHorizonDays = 14

// MaxHorizonDays bounds the query. A caller asking for a year gets a list
// nobody reads, at the cost of a scan nobody wanted.
const MaxHorizonDays = 180

// MilestoneLoad is a milestone with what is under it.
type MilestoneLoad struct {
	Milestone *pl.Milestone
	Stat      repo.MilestoneStat
}

// Dashboard is the project view's opening question, answered in one round trip.
type Dashboard struct {
	Totals      repo.Totals
	Milestones  []MilestoneLoad
	Assignees   []repo.AssigneeStat
	Upcoming    []repo.DatedItem
	Blocked     []repo.BlockedItem
	Warnings    []pl.Warning
	HorizonDays int
}

// Dashboard computes a project's rollup.
//
// One endpoint rather than six, because a frontend assembling this from six
// calls is a frontend rendering six moments — and the one that matters, "is
// anything overdue", would be the one that arrives last. Nothing here is stored
// and nothing is cached: every figure is a query against live rows, for exactly
// the reason warnings are (§3.5).
func (s *Service) Dashboard(ctx context.Context, projectID uuid.UUID, horizonDays int, p policy.Principal) (*Dashboard, error) {
	if err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	if horizonDays <= 0 {
		horizonDays = DefaultHorizonDays
	}
	if horizonDays > MaxHorizonDays {
		horizonDays = MaxHorizonDays
	}

	q := s.db.Pool
	out := &Dashboard{HorizonDays: horizonDays}

	totals, err := s.dashboards.Counts(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	out.Totals = totals

	milestones, err := s.milestones.ListByProject(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	stats, err := s.dashboards.MilestoneRollup(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	for _, m := range milestones {
		out.Milestones = append(out.Milestones, MilestoneLoad{Milestone: m, Stat: stats[m.ID]})
	}

	if out.Assignees, err = s.dashboards.AssigneeLoad(ctx, q, projectID); err != nil {
		return nil, err
	}
	if out.Upcoming, err = s.dashboards.Upcoming(ctx, q, projectID, horizonDays); err != nil {
		return nil, err
	}
	if out.Blocked, err = s.dashboards.Blocked(ctx, q, projectID); err != nil {
		return nil, err
	}
	// The same computation the warnings endpoint runs, from the same repo. A
	// dashboard with its own idea of what a date violation is would be a second
	// rulebook that disagrees with the first exactly when somebody is checking.
	if out.Warnings, err = s.warnings.ForProject(ctx, q, projectID); err != nil {
		return nil, err
	}
	return out, nil
}
