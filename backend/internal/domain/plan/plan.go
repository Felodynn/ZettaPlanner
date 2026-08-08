package plan

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
)

// ── the objects ──────────────────────────────────────────────────────────────

// Milestone groups work items under a date and an intent (CONTRACT §3.5).
type Milestone struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	Name      string
	Intent    string
	Seq       int
	StartDate *time.Time
	DueDate   *time.Time
	Status    MilestoneStatus

	Provenance domain.Provenance
	ActorID    uuid.UUID
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
}

// WorkItem is the unit of plan (CONTRACT §3.5).
//
// Ref is the human-facing handle — ZP-14 — and is what people and models
// actually say. It is issued once and never reissued, including after the item
// is deleted: an audit entry or a worklog mentioning ZP-14 must name exactly one
// thing forever.
type WorkItem struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	MilestoneID *uuid.UUID
	Ref         string
	Title       string
	Intent      string
	Acceptance  string
	Status      Status
	ProgressPct int
	Priority    Priority
	AssigneeID  *uuid.UUID
	StartDate   *time.Time
	DueDate     *time.Time

	Provenance domain.Provenance
	ActorID    uuid.UUID
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
}

// SubTask is a leaf. There is no deeper nesting on purpose: a tree of arbitrary
// depth is a tree nobody agrees on the shape of, and every query over it has to
// choose a depth anyway.
type SubTask struct {
	ID         uuid.UUID
	WorkItemID uuid.UUID
	Title      string
	Status     Status
	Seq        int
	AssigneeID *uuid.UUID

	Provenance domain.Provenance
	ActorID    uuid.UUID
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
}

// Dependency is a directed edge between two work items.
type Dependency struct {
	FromItemID uuid.UUID
	ToItemID   uuid.UUID
	Kind       DependencyKind
	ActorID    uuid.UUID
	CreatedAt  time.Time

	// FromRef and ToRef are filled by the queries that read dependencies for
	// display. A UUID is useless to a human and worse than useless to a model.
	FromRef   string
	ToRef     string
	FromTitle string
	ToTitle   string
}

// EntityRef is a work item's soft reference to a piece of architecture.
//
// Set by a human or an agent, **never derived** (§3.5). It exists purely so
// context assembly can pull the relevant architecture slice when the item is
// discussed.
type EntityRef struct {
	WorkItemID uuid.UUID
	EntityID   uuid.UUID
	Relation   RefRelation
	ActorID    uuid.UUID

	// EntitySlug and EntityName are filled on read, for the same reason as
	// Dependency.FromRef.
	EntitySlug string
	EntityName string
}

// ChecklistItem is a box on a work item.
//
// CheckedBy and CheckedAt are kept even after the box is cleared, so that "who
// ticked this and when" survives an un-tick. The un-tick itself is a transition
// in the audit log; this pair is only the current state.
type ChecklistItem struct {
	ID         uuid.UUID
	WorkItemID uuid.UUID
	Text       string
	Checked    bool
	CheckedBy  *uuid.UUID
	CheckedAt  *time.Time
	Seq        int
	DeletedAt  *time.Time
}

// Worklog is an append-only entry. Never deleted, and corrected by adding
// another (§6.5 constraint 1) — that is what makes the log evidence rather than
// a current-state field.
type Worklog struct {
	ID         uuid.UUID
	ProjectID  uuid.UUID
	WorkItemID *uuid.UUID
	ActorID    uuid.UUID
	Kind       WorklogKind
	Body       string
	CreatedAt  time.Time

	// ItemRef and ActorName are filled on read.
	ItemRef   string
	ActorName string
}

// Deviation is a recorded divergence between the plan and reality.
//
// Split ownership (§4.2): the reporter owns the summary and the severity,
// because they are the one who saw it; a manager owns the resolution, because
// deciding what to do about it is a planning act.
type Deviation struct {
	ID         uuid.UUID
	ProjectID  uuid.UUID
	WorkItemID *uuid.UUID
	WorklogID  *uuid.UUID
	Summary    string
	Severity   Severity
	Resolution string
	ResolvedAt *time.Time
	ActorID    uuid.UUID
	CreatedAt  time.Time
	DeletedAt  *time.Time

	ItemRef string
}

// Resolved reports whether the deviation has been closed out.
func (d *Deviation) Resolved() bool { return d.ResolvedAt != nil }

// ── refs ─────────────────────────────────────────────────────────────────────

// Ref builds a work item's human-facing reference from the project key and the
// project's monotonic counter.
//
// The counter is the authority, not max(existing)+1. A ref is never reissued
// even if every work item in the project is deleted, because the audit log,
// every worklog and every sentence anyone has written about ZP-14 would
// otherwise become ambiguous with no way to disambiguate them later.
func Ref(projectKey string, seq int) string {
	return fmt.Sprintf("%s-%d", projectKey, seq)
}

// ── dates ────────────────────────────────────────────────────────────────────

// DateLayout is the only date format this system accepts or emits. Plan dates
// are calendar days, not instants: "due 2026-08-08" does not mean midnight in
// the server's timezone, and storing it as one produces off-by-one days for
// everybody not sitting on the server.
const DateLayout = "2006-01-02"

// ParseDate turns a wire date into a day. The empty string means "clear it",
// which is why the result is a pointer and the second return says which.
func ParseDate(s string) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	t, err := time.Parse(DateLayout, strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not a date; write it as YYYY-MM-DD, for example 2026-08-08", domain.ErrInvalid, s)
	}
	return &t, nil
}

// FormatDate renders a day for the wire, or "" for an unset one.
func FormatDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(DateLayout)
}

// ValidateDates rejects a range that runs backwards.
//
// Checked, not silently swapped. A start after a due date is almost always a
// typo in one of the two, and the system cannot tell which — quietly reordering
// them would move a date nobody asked to move.
func ValidateDates(what string, start, due *time.Time) error {
	if start == nil || due == nil {
		return nil
	}
	if start.After(*due) {
		return fmt.Errorf("%w: this %s starts on %s and is due on %s, which is earlier. "+
			"One of the two dates is a typo, and the system cannot tell which — fix the one that is wrong",
			domain.ErrInvalid, what, FormatDate(start), FormatDate(due))
	}
	return nil
}

// ── invariants ───────────────────────────────────────────────────────────────

// ValidateMilestone checks everything that must hold before a milestone row is
// written.
func ValidateMilestone(m *Milestone) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("%w: a milestone needs a name", domain.ErrInvalid)
	}
	if err := ValidateMilestoneStatus(m.Status); err != nil {
		return err
	}
	if !m.Provenance.Valid() {
		return fmt.Errorf("%w: %q is not a known origin", domain.ErrInvalid, m.Provenance)
	}
	return ValidateDates("milestone", m.StartDate, m.DueDate)
}

// ValidateWorkItem checks everything that must hold before a work item row is
// written.
func ValidateWorkItem(w *WorkItem) error {
	if strings.TrimSpace(w.Title) == "" {
		return fmt.Errorf("%w: a work item needs a title", domain.ErrInvalid)
	}
	if err := ValidateStatus(w.Status); err != nil {
		return err
	}
	if err := ValidatePriority(w.Priority); err != nil {
		return err
	}
	if w.ProgressPct < 0 || w.ProgressPct > 100 {
		return fmt.Errorf("%w: progress_pct is %d; it is a percentage, so it runs from 0 to 100",
			domain.ErrInvalid, w.ProgressPct)
	}
	if !w.Provenance.Valid() {
		return fmt.Errorf("%w: %q is not a known origin", domain.ErrInvalid, w.Provenance)
	}
	return ValidateDates("work item", w.StartDate, w.DueDate)
}

// ValidateSubTask checks a subtask's invariants.
func ValidateSubTask(s *SubTask) error {
	if strings.TrimSpace(s.Title) == "" {
		return fmt.Errorf("%w: a subtask needs a title", domain.ErrInvalid)
	}
	if err := ValidateStatus(s.Status); err != nil {
		return err
	}
	if !s.Provenance.Valid() {
		return fmt.Errorf("%w: %q is not a known origin", domain.ErrInvalid, s.Provenance)
	}
	return nil
}

// ValidateChecklistItem checks a checklist item's invariants.
func ValidateChecklistItem(c *ChecklistItem) error {
	if strings.TrimSpace(c.Text) == "" {
		return fmt.Errorf("%w: a checklist item needs text", domain.ErrInvalid)
	}
	return nil
}

// ValidateWorklog checks a worklog's invariants.
func ValidateWorklog(w *Worklog) error {
	if strings.TrimSpace(w.Body) == "" {
		return fmt.Errorf("%w: a worklog with an empty body records nothing", domain.ErrInvalid)
	}
	return ValidateWorklogKind(w.Kind)
}

// ValidateDeviation checks a deviation's invariants.
func ValidateDeviation(d *Deviation) error {
	if strings.TrimSpace(d.Summary) == "" {
		return fmt.Errorf("%w: a deviation needs a summary saying what diverged", domain.ErrInvalid)
	}
	return ValidateSeverity(d.Severity)
}
