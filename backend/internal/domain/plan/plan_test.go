package plan

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
)

func day(t *testing.T, s string) *time.Time {
	t.Helper()
	d, err := ParseDate(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// ── vocabularies ─────────────────────────────────────────────────────────────

// TestTeachingErrorsListEveryValueWithItsMeaning is the property the whole
// closed-vocabulary design rests on. The text is read by the planner agent when
// a tool call is rejected, and a bare refusal produces a second invalid value.
func TestTeachingErrorsListEveryValueWithItsMeaning(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want []string
	}{
		{"status", ValidateStatus("in_review"),
			[]string{"todo", "in_progress", "blocked", "review", "done", "dropped", "waiting on someone else"}},
		{"milestone status", ValidateMilestoneStatus("blocked"),
			[]string{"planned", "active", "done", "dropped", "delivered"}},
		{"priority", ValidatePriority("critical"),
			[]string{"low", "normal", "high", "urgent", "displaces work in progress"}},
		{"severity", ValidateSeverity("catastrophic"),
			[]string{"low", "medium", "high"}},
		{"worklog kind", ValidateWorklogKind("chatter"),
			[]string{"progress", "deviation", "note"}},
		{"dependency kind", ValidateDependencyKind("precedes"),
			[]string{"blocks", "relates", "acyclic"}},
		{"entity ref relation", ValidateRefRelation("touches"),
			[]string{"implements"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("expected the value to be rejected")
			}
			msg := tc.err.Error()
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("the refusal never mentions %q:\n%s", want, msg)
				}
			}
			// It must also answer to the generic sentinel, so a caller that
			// only wants to know whose fault it was need not know the type.
			if !errors.Is(tc.err, domain.ErrInvalid) {
				t.Error("a vocabulary error should satisfy errors.Is(err, domain.ErrInvalid)")
			}
		})
	}
}

// TestUnsetPriorityIsNotNormal: an untriaged item and one explicitly judged
// ordinary are different facts, and the empty value is how the first is stored.
func TestUnsetPriorityIsNotNormal(t *testing.T) {
	if err := ValidatePriority(""); err != nil {
		t.Errorf("an unset priority must be accepted: %v", err)
	}
	if PriorityNormal == "" {
		t.Error("normal must not be the empty value, or the two facts collapse")
	}
}

// TestClosedStatusesFollowOpen guards the SQL/Go seam: the warning queries take
// their exclusion list from here, so a status renamed in this file must not
// leave a query silently matching nothing.
func TestClosedStatusesFollowOpen(t *testing.T) {
	closed := ClosedStatuses()
	for _, s := range Statuses() {
		want := !s.Open()
		var found bool
		for _, c := range closed {
			if c == string(s) {
				found = true
			}
		}
		if found != want {
			t.Errorf("%s: Open()=%v but ClosedStatuses() %s it", s, s.Open(),
				map[bool]string{true: "includes", false: "omits"}[found])
		}
	}
	if len(ClosedMilestoneStatuses()) != 2 {
		t.Errorf("expected done and dropped to be the closed milestone statuses, got %v", ClosedMilestoneStatuses())
	}
}

// ── dates ────────────────────────────────────────────────────────────────────

func TestParseDate(t *testing.T) {
	if d, err := ParseDate(""); err != nil || d != nil {
		t.Errorf("the empty string means clear it, got %v %v", d, err)
	}
	if d, err := ParseDate(" 2026-08-08 "); err != nil || FormatDate(d) != "2026-08-08" {
		t.Errorf("ParseDate = %v %v", FormatDate(d), err)
	}

	err := func() error { _, e := ParseDate("next tuesday"); return e }()
	if err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("a bad date should say what one looks like: %v", err)
	}
	if !errors.Is(err, domain.ErrInvalid) {
		t.Error("a bad date is the caller's fault and should say so")
	}
}

// TestBackwardsRangeIsRefusedNotReordered: one of the two dates is a typo and
// the system cannot tell which. Swapping them would move a date nobody asked to
// move.
func TestBackwardsRangeIsRefusedNotReordered(t *testing.T) {
	start, due := day(t, "2026-09-01"), day(t, "2026-08-01")
	err := ValidateDates("work item", start, due)
	if err == nil {
		t.Fatal("a range running backwards must be refused")
	}
	if !strings.Contains(err.Error(), "typo") {
		t.Errorf("the message should name the ambiguity: %v", err)
	}
	if FormatDate(start) != "2026-09-01" || FormatDate(due) != "2026-08-01" {
		t.Error("ValidateDates modified its arguments")
	}
	// One end unset is not a violation. Half a range is a normal state.
	if err := ValidateDates("work item", start, nil); err != nil {
		t.Errorf("an unset due date is not a violation: %v", err)
	}
}

// ── warnings ─────────────────────────────────────────────────────────────────

func TestWarningsNameBothEndsAndTheGap(t *testing.T) {
	blocker := &WorkItem{ID: uuid.Must(uuid.NewV7()), Ref: "ZP-1", Title: "Schema", DueDate: day(t, "2026-08-20")}
	blocked := &WorkItem{ID: uuid.Must(uuid.NewV7()), Ref: "ZP-2", Title: "Handlers", StartDate: day(t, "2026-08-10")}

	w := BlockerDateWarning(blocked, blocker)
	if w.Kind != WarnBlockerDate || w.RelatedKind != "work_item" {
		t.Errorf("wrong shape: %+v", w)
	}
	if w.Days != 10 {
		t.Errorf("days = %d, want 10", w.Days)
	}
	for _, want := range []string{"ZP-1", "ZP-2", "2026-08-10", "2026-08-20", "10 days"} {
		if !strings.Contains(w.Message, want) {
			t.Errorf("the message never mentions %q: %s", want, w.Message)
		}
	}
	// The message must say that nothing was rescheduled. A warning that reads
	// like a report of an adjustment is worse than none.
	if !strings.Contains(w.Message, "Nothing has been rescheduled") {
		t.Errorf("the message should say no cascade ran: %s", w.Message)
	}

	m := &Milestone{ID: uuid.Must(uuid.NewV7()), Name: "v0 backend", DueDate: day(t, "2026-09-30")}
	late := &WorkItem{ID: uuid.Must(uuid.NewV7()), Ref: "ZP-3", Title: "Docs", DueDate: day(t, "2026-10-15")}
	mw := MilestoneDateWarning(late, m)
	if mw.Days != 15 || mw.RelatedKind != "milestone" {
		t.Errorf("wrong shape: %+v", mw)
	}
	for _, want := range []string{"ZP-3", "v0 backend", "15 days"} {
		if !strings.Contains(mw.Message, want) {
			t.Errorf("the message never mentions %q: %s", want, mw.Message)
		}
	}
}

// TestSortWarningsIsDeterministic: two identical requests returning the same
// list in a different order is a diff in every client that renders it, and a
// context assembly that changes between turns for no reason.
func TestSortWarningsIsDeterministic(t *testing.T) {
	in := []Warning{
		{Kind: WarnMilestoneDate, ItemRef: "ZP-9", Days: 2},
		{Kind: WarnBlockerDate, ItemRef: "ZP-4", Days: 30},
		{Kind: WarnBlockerDate, ItemRef: "ZP-2", Days: 2},
	}
	SortWarnings(in)
	if in[0].ItemRef != "ZP-4" {
		t.Errorf("the worst overrun should come first, got %s", in[0].ItemRef)
	}
	if in[1].ItemRef != "ZP-2" || in[2].ItemRef != "ZP-9" {
		t.Errorf("ties should break by kind then ref, got %s then %s", in[1].ItemRef, in[2].ItemRef)
	}
}

// ── cycles ───────────────────────────────────────────────────────────────────

func TestCycleErrorNamesTheRingAndTheWayOut(t *testing.T) {
	err := &CycleError{Path: []string{"ZP-3", "ZP-7", "ZP-9", "ZP-3"}}
	msg := err.Error()
	for _, want := range []string{"ZP-3", "ZP-7", "ZP-9", "→"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message never mentions %q: %s", want, msg)
		}
	}
	// Lead with the route forward: a refusal that leads with the reason gets
	// prose back, and one that names the action gets the action.
	if !strings.Contains(msg, "relates") {
		t.Errorf("the message should offer the way out: %s", msg)
	}
	if !errors.Is(err, domain.ErrInvalid) {
		t.Error("a cycle is the caller's fault and should say so")
	}
}
