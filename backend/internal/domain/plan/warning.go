package plan

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Warnings are **computed, never stored** (BACKEND.md Phase 4).
//
// A stored warning is stale the moment a date moves, and the staleness is
// invisible: a row saying "ZP-14 starts before its blocker is due" looks exactly
// the same whether it is true now or was true last month. Recomputing costs one
// query.
//
// And a warning is all it is. There is **no auto-scheduling**: moving a
// blocker's due date produces this, never a cascade of adjusted dates
// downstream. A cascade that is right 90% of the time silently destroys the 10%
// somebody set by hand, and they find out when the date they chose has quietly
// become a different date.

// WarningKind is the closed set of violations v0 computes.
type WarningKind string

const (
	// WarnBlockerDate: an item is scheduled to start before the item blocking
	// it is due to finish.
	WarnBlockerDate WarningKind = "blocker_date"
	// WarnMilestoneDate: an item is due after the milestone it belongs to.
	WarnMilestoneDate WarningKind = "milestone_date"
)

// WarningKinds returns every kind computed in v0.
func WarningKinds() []WarningKind { return []WarningKind{WarnBlockerDate, WarnMilestoneDate} }

// Warning is one computed violation.
//
// It carries both ends with their human-facing names, because the reader — a
// person or the planner agent — needs to know which two things disagree and by
// how much, not merely that something does.
type Warning struct {
	Kind WarningKind

	ItemID    uuid.UUID
	ItemRef   string
	ItemTitle string
	ItemDate  *time.Time

	// RelatedKind is "work_item" for a blocker and "milestone" for a milestone
	// overrun.
	RelatedKind string
	RelatedID   uuid.UUID
	RelatedRef  string
	RelatedName string
	RelatedDate *time.Time

	// Days is how far the violation runs, always positive. The number is what
	// makes a list of warnings triageable; without it every row looks equally
	// urgent.
	Days int

	Message string
}

// BlockerDateWarning builds the warning for an item starting before its blocker
// is due.
func BlockerDateWarning(item, blocker *WorkItem) Warning {
	w := Warning{
		Kind:        WarnBlockerDate,
		ItemID:      item.ID,
		ItemRef:     item.Ref,
		ItemTitle:   item.Title,
		ItemDate:    item.StartDate,
		RelatedKind: "work_item",
		RelatedID:   blocker.ID,
		RelatedRef:  blocker.Ref,
		RelatedName: blocker.Title,
		RelatedDate: blocker.DueDate,
		Days:        daysBetween(item.StartDate, blocker.DueDate),
	}
	w.Message = fmt.Sprintf(
		"%s is set to start on %s, but %s, which blocks it, is not due until %s — %s later. "+
			"Nothing has been rescheduled: move whichever of the two dates is wrong.",
		item.Ref, FormatDate(item.StartDate), blocker.Ref, FormatDate(blocker.DueDate), plural(w.Days))
	return w
}

// MilestoneDateWarning builds the warning for an item due after its milestone.
func MilestoneDateWarning(item *WorkItem, m *Milestone) Warning {
	w := Warning{
		Kind:        WarnMilestoneDate,
		ItemID:      item.ID,
		ItemRef:     item.Ref,
		ItemTitle:   item.Title,
		ItemDate:    item.DueDate,
		RelatedKind: "milestone",
		RelatedID:   m.ID,
		RelatedName: m.Name,
		RelatedDate: m.DueDate,
		Days:        daysBetween(m.DueDate, item.DueDate),
	}
	w.Message = fmt.Sprintf(
		"%s is due on %s, which is %s after its milestone %q closes on %s. "+
			"Either the item moves, the milestone moves, or the item belongs to a later one.",
		item.Ref, FormatDate(item.DueDate), plural(w.Days), m.Name, FormatDate(m.DueDate))
	return w
}

// SortWarnings puts warnings in a stable, useful order: worst overrun first,
// then by kind, then by ref.
//
// Stable rather than merely sorted. Two identical requests returning the same
// list in a different order is a diff in every client that renders it, and a
// context assembly that changes between turns for no reason.
func SortWarnings(in []Warning) {
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].Days != in[j].Days {
			return in[i].Days > in[j].Days
		}
		if in[i].Kind != in[j].Kind {
			return in[i].Kind < in[j].Kind
		}
		if in[i].ItemRef != in[j].ItemRef {
			return in[i].ItemRef < in[j].ItemRef
		}
		return in[i].RelatedRef < in[j].RelatedRef
	})
}

// daysBetween is later minus earlier, in whole days, floored at zero. Both
// dates are calendar days at UTC midnight, so the subtraction is exact.
func daysBetween(earlier, later *time.Time) int {
	if earlier == nil || later == nil {
		return 0
	}
	d := int(later.Sub(*earlier).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

func plural(days int) string {
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}
