// Package plan holds the plan's pure types and their invariants: milestones,
// work items, subtasks, dependencies, checklists, worklogs and deviations.
//
// The plan is **independent of the diagram** (CONTRACT §3.5). No agent derives
// plan structure from a diagram and none derives diagram structure from the
// plan, so this package knows about the architecture graph only through
// `item_entity_refs` — soft references set explicitly by a human or an agent,
// never computed. A derivation that is wrong 10% of the time is a permanent
// source of confusion with no permanent fix.
//
// Nothing here does I/O. Every rule in this file can be checked in a test with
// no database, which is what makes the rules cheap enough to actually have.
package plan

import (
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
)

// ── work item and subtask status ─────────────────────────────────────────────

// Status is the lifecycle of a work item, and of a subtask.
//
// One vocabulary at both levels on purpose. It is one thing to teach the
// planner agent instead of two, and it makes "how long did this spend in each
// status" the same window function over `*.status_changed` whichever level is
// being asked about (§6.5 constraint 2).
type Status string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusReview     Status = "review"
	StatusDone       Status = "done"
	StatusDropped    Status = "dropped"
)

var statusMeaning = map[Status]string{
	StatusTodo:       "accepted into the plan, not started",
	StatusInProgress: "someone is working on it now",
	StatusBlocked:    "started and stopped by something outside the item; say what in a worklog",
	StatusReview:     "the work is done and is waiting on someone else to look at it",
	StatusDone:       "finished and accepted",
	StatusDropped:    "deliberately not being done; kept so the decision stays visible",
}

// Statuses returns every status, in lifecycle order.
func Statuses() []Status {
	return []Status{StatusTodo, StatusInProgress, StatusBlocked, StatusReview, StatusDone, StatusDropped}
}

// Valid reports whether s is in the closed set.
func (s Status) Valid() bool { _, ok := statusMeaning[s]; return ok }

// StatusMeaning returns the one-line gloss.
func StatusMeaning(s Status) string { return statusMeaning[s] }

func (s Status) String() string { return string(s) }

// Open reports whether the item still represents outstanding work. Used by the
// warning queries and by context assembly: a date violation on something
// already finished is history, not a warning.
func (s Status) Open() bool { return s != StatusDone && s != StatusDropped }

// ClosedStatuses returns the statuses that mean no work remains, as strings.
//
// Exported for the warning queries, which have to exclude finished work in SQL.
// Deriving the list here rather than writing 'done', 'dropped' into the query
// means a status renamed in this file cannot leave a query silently matching
// nothing.
func ClosedStatuses() []string {
	var out []string
	for _, s := range Statuses() {
		if !s.Open() {
			out = append(out, string(s))
		}
	}
	return out
}

// ValidateStatus returns nil or a teaching error.
func ValidateStatus(s Status) error {
	if s.Valid() {
		return nil
	}
	return &domain.VocabularyError{
		Field: "status",
		Value: string(s),
		Valid: domain.Glossed(Statuses(), StatusMeaning),
		Hint: "`dropped` is not a delete: use it when the decision not to do something is worth keeping. " +
			"Deleting removes the item from every view while leaving its history queryable.",
	}
}

// ── milestone status ─────────────────────────────────────────────────────────

// MilestoneStatus is deliberately coarser than Status.
//
// A milestone is not `blocked` or `review` — those are properties of the items
// underneath it. Offering them would invite a milestone whose status
// contradicts its own contents, and nothing would ever reconcile the two.
type MilestoneStatus string

const (
	MilestonePlanned MilestoneStatus = "planned"
	MilestoneActive  MilestoneStatus = "active"
	MilestoneDone    MilestoneStatus = "done"
	MilestoneDropped MilestoneStatus = "dropped"
)

var milestoneStatusMeaning = map[MilestoneStatus]string{
	MilestonePlanned: "agreed and scheduled; no work has started",
	MilestoneActive:  "work is under way somewhere inside it",
	MilestoneDone:    "delivered",
	MilestoneDropped: "abandoned; kept so the decision stays visible",
}

// MilestoneStatuses returns every milestone status, in lifecycle order.
func MilestoneStatuses() []MilestoneStatus {
	return []MilestoneStatus{MilestonePlanned, MilestoneActive, MilestoneDone, MilestoneDropped}
}

// Valid reports whether s is in the closed set.
func (s MilestoneStatus) Valid() bool { _, ok := milestoneStatusMeaning[s]; return ok }

// MilestoneStatusMeaning returns the one-line gloss.
func MilestoneStatusMeaning(s MilestoneStatus) string { return milestoneStatusMeaning[s] }

func (s MilestoneStatus) String() string { return string(s) }

// Open reports whether the milestone still represents outstanding work.
func (s MilestoneStatus) Open() bool { return s != MilestoneDone && s != MilestoneDropped }

// ClosedMilestoneStatuses returns the milestone statuses that mean no work
// remains, as strings for SQL. See ClosedStatuses.
func ClosedMilestoneStatuses() []string {
	var out []string
	for _, s := range MilestoneStatuses() {
		if !s.Open() {
			out = append(out, string(s))
		}
	}
	return out
}

// ValidateMilestoneStatus returns nil or a teaching error.
func ValidateMilestoneStatus(s MilestoneStatus) error {
	if s.Valid() {
		return nil
	}
	return &domain.VocabularyError{
		Field: "milestone status",
		Value: string(s),
		Valid: domain.Glossed(MilestoneStatuses(), MilestoneStatusMeaning),
		Hint: "A milestone has no `blocked` or `review`: those are facts about the work items inside it, " +
			"and a milestone status that contradicts its own contents is worse than no status.",
	}
}

// ── priority ─────────────────────────────────────────────────────────────────

// Priority is optional. Unset is not `normal`: an item nobody has triaged and an
// item explicitly judged ordinary are different facts, and collapsing them
// erases the queue that triage produces.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

var priorityMeaning = map[Priority]string{
	PriorityLow:    "worth doing, and nothing breaks if it slips",
	PriorityNormal: "the default once someone has actually looked at it",
	PriorityHigh:   "should be picked up before anything normal",
	PriorityUrgent: "displaces work in progress",
}

// Priorities returns every priority, lowest first.
func Priorities() []Priority {
	return []Priority{PriorityLow, PriorityNormal, PriorityHigh, PriorityUrgent}
}

// Valid reports whether p is in the closed set.
func (p Priority) Valid() bool { _, ok := priorityMeaning[p]; return ok }

// PriorityMeaning returns the one-line gloss.
func PriorityMeaning(p Priority) string { return priorityMeaning[p] }

func (p Priority) String() string { return string(p) }

// ValidatePriority returns nil or a teaching error. The empty string is
// accepted: it is how "nobody has triaged this" is stored.
func ValidatePriority(p Priority) error {
	if p == "" || p.Valid() {
		return nil
	}
	return &domain.VocabularyError{
		Field: "priority",
		Value: string(p),
		Valid: domain.Glossed(Priorities(), PriorityMeaning),
		Hint:  "Leave it unset rather than inventing a level; unset means nobody has triaged it yet, which is itself worth knowing.",
	}
}

// ── worklog kind ─────────────────────────────────────────────────────────────

// WorklogKind separates the three things people write into the log, because the
// retrospective reads them separately.
type WorklogKind string

const (
	WorklogProgress  WorklogKind = "progress"
	WorklogDeviation WorklogKind = "deviation"
	WorklogNote      WorklogKind = "note"
)

var worklogKindMeaning = map[WorklogKind]string{
	WorklogProgress:  "what was done, and what it turned out to involve",
	WorklogDeviation: "reality diverged from the plan; usually paired with a Deviation",
	WorklogNote:      "context worth keeping that is neither progress nor a divergence",
}

// WorklogKinds returns every kind.
func WorklogKinds() []WorklogKind {
	return []WorklogKind{WorklogProgress, WorklogDeviation, WorklogNote}
}

// Valid reports whether k is in the closed set.
func (k WorklogKind) Valid() bool { _, ok := worklogKindMeaning[k]; return ok }

// WorklogKindMeaning returns the one-line gloss.
func WorklogKindMeaning(k WorklogKind) string { return worklogKindMeaning[k] }

func (k WorklogKind) String() string { return string(k) }

// ValidateWorklogKind returns nil or a teaching error.
func ValidateWorklogKind(k WorklogKind) error {
	if k.Valid() {
		return nil
	}
	return &domain.VocabularyError{
		Field: "worklog kind",
		Value: string(k),
		Valid: domain.Glossed(WorklogKinds(), WorklogKindMeaning),
		Hint:  "Worklogs are append-only in practice: correct one by adding another, which is what makes the log evidence rather than a current-state field.",
	}
}

// ── deviation severity ───────────────────────────────────────────────────────

// Severity is how far reality diverged.
type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

var severityMeaning = map[Severity]string{
	SeverityLow:    "worth recording; the plan still stands",
	SeverityMedium: "the plan needs adjusting somewhere",
	SeverityHigh:   "the plan is wrong and work is proceeding on a false premise",
}

// Severities returns every severity, lowest first.
func Severities() []Severity { return []Severity{SeverityLow, SeverityMedium, SeverityHigh} }

// Valid reports whether s is in the closed set.
func (s Severity) Valid() bool { _, ok := severityMeaning[s]; return ok }

// SeverityMeaning returns the one-line gloss.
func SeverityMeaning(s Severity) string { return severityMeaning[s] }

func (s Severity) String() string { return string(s) }

// ValidateSeverity returns nil or a teaching error.
func ValidateSeverity(s Severity) error {
	if s.Valid() {
		return nil
	}
	return &domain.VocabularyError{
		Field: "severity",
		Value: string(s),
		Valid: domain.Glossed(Severities(), SeverityMeaning),
	}
}

// ── dependency kind ──────────────────────────────────────────────────────────

// DependencyKind is the edge between two work items. Both are directed.
type DependencyKind string

const (
	// DepBlocks: the source must finish before the target can proceed. This is
	// the only kind cycles are rejected on, because it is the only one that
	// asserts an order.
	DepBlocks DependencyKind = "blocks"
	// DepRelates: worth reading together. Carries no ordering, so a cycle in it
	// is not a contradiction and is not rejected.
	DepRelates DependencyKind = "relates"
)

var dependencyKindMeaning = map[DependencyKind]string{
	DepBlocks:  "the source must finish before the target can proceed; must stay acyclic",
	DepRelates: "worth reading together; no ordering is implied, so cycles are fine",
}

// DependencyKinds returns both kinds.
func DependencyKinds() []DependencyKind { return []DependencyKind{DepBlocks, DepRelates} }

// Valid reports whether k is in the closed set.
func (k DependencyKind) Valid() bool { _, ok := dependencyKindMeaning[k]; return ok }

// DependencyKindMeaning returns the one-line gloss.
func DependencyKindMeaning(k DependencyKind) string { return dependencyKindMeaning[k] }

func (k DependencyKind) String() string { return string(k) }

// ValidateDependencyKind returns nil or a teaching error.
func ValidateDependencyKind(k DependencyKind) error {
	if k.Valid() {
		return nil
	}
	return &domain.VocabularyError{
		Field: "dependency kind",
		Value: string(k),
		Valid: domain.Glossed(DependencyKinds(), DependencyKindMeaning),
		Hint:  "Dependencies are edges between work items, not a tree; an item may block many and be blocked by many.",
	}
}

// ── entity reference relation ────────────────────────────────────────────────

// RefRelation is how a work item points at a piece of architecture.
//
// One value in v0. It is still a closed vocabulary rather than free text
// because the column is part of a primary key: the moment a second spelling
// appears, "which items touch the token store" silently starts missing rows.
type RefRelation string

const RefImplements RefRelation = "implements"

// RefRelations returns every relation.
func RefRelations() []RefRelation { return []RefRelation{RefImplements} }

// Valid reports whether r is in the closed set.
func (r RefRelation) Valid() bool { return r == RefImplements }

// RefRelationMeaning returns the one-line gloss.
func RefRelationMeaning(r RefRelation) string {
	if r == RefImplements {
		return "this item builds, changes, or removes that piece of architecture"
	}
	return ""
}

func (r RefRelation) String() string { return string(r) }

// ValidateRefRelation returns nil or a teaching error.
func ValidateRefRelation(r RefRelation) error {
	if r.Valid() {
		return nil
	}
	return &domain.VocabularyError{
		Field: "entity reference relation",
		Value: string(r),
		Valid: domain.Glossed(RefRelations(), RefRelationMeaning),
		Hint: "Entity refs are set explicitly and never derived (§3.5). They exist so context assembly can pull " +
			"the relevant architecture slice when this item is discussed — not to describe the architecture itself.",
	}
}
