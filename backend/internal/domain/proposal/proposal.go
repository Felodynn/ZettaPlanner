// Package proposal holds governance's pure types.
//
// A Proposal is how anything the caller does not own gets changed (§5.1). It
// carries a title, a **rationale**, and an ordered list of typed changes, and
// it is applied atomically as one revision or not at all — never partially,
// never auto-merged.
//
// Phase 6 creates them. Phase 7 decides and applies them. The split matters
// here: an agent's only route to a manager-owned field is to raise one of
// these, and that is enforced by there being no tool that writes such a field
// (BACKEND.md Phase 6), not by a tool that refuses.
package proposal

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
)

// ── status ───────────────────────────────────────────────────────────────────

// Status is a proposal's lifecycle: pending → approved | rejected | superseded.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
	// StatusSuperseded is what a proposal becomes when the state it was raised
	// against has moved. Not partially applied and not auto-merged: the
	// conflict is reported and a human decides again.
	StatusSuperseded Status = "superseded"
)

var statusMeaning = map[Status]string{
	StatusPending:    "raised and waiting on a decision",
	StatusApproved:   "accepted and applied as one revision",
	StatusRejected:   "refused, with a rationale that becomes context (§5.2)",
	StatusSuperseded: "the state it was raised against moved; nothing was applied",
}

// Statuses returns every status.
func Statuses() []Status {
	return []Status{StatusPending, StatusApproved, StatusRejected, StatusSuperseded}
}

// Valid reports whether s is known.
func (s Status) Valid() bool { _, ok := statusMeaning[s]; return ok }

// StatusMeaning returns the one-line gloss.
func StatusMeaning(s Status) string { return statusMeaning[s] }

// Decided reports whether the proposal is no longer pending.
func (s Status) Decided() bool { return s != StatusPending }

func (s Status) String() string { return string(s) }

// ── target ───────────────────────────────────────────────────────────────────

// TargetKind is what a proposal touches, taken as a whole.
type TargetKind string

const (
	TargetDiagram TargetKind = "diagram"
	TargetPlan    TargetKind = "plan"
	TargetMixed   TargetKind = "mixed"
)

var targetMeaning = map[TargetKind]string{
	TargetDiagram: "the architecture graph only",
	TargetPlan:    "milestones, work items and their fields only",
	TargetMixed:   "both, in one atomic decision",
}

// TargetKinds returns every kind.
func TargetKinds() []TargetKind { return []TargetKind{TargetDiagram, TargetPlan, TargetMixed} }

// Valid reports whether k is known.
func (k TargetKind) Valid() bool { _, ok := targetMeaning[k]; return ok }

// TargetMeaning returns the one-line gloss.
func TargetMeaning(k TargetKind) string { return targetMeaning[k] }

func (k TargetKind) String() string { return string(k) }

// ── changes ──────────────────────────────────────────────────────────────────

// Op is what one change does.
type Op string

const (
	OpCreate Op = "create"
	OpUpdate Op = "update"
	OpDelete Op = "delete"
)

var opMeaning = map[Op]string{
	OpCreate: "make something that does not exist yet",
	OpUpdate: "change named fields on something that does; `before` is what they held when this was raised",
	OpDelete: "soft-delete something; its history stays queryable",
}

// Ops returns every op.
func Ops() []Op { return []Op{OpCreate, OpUpdate, OpDelete} }

// Valid reports whether o is known.
func (o Op) Valid() bool { _, ok := opMeaning[o]; return ok }

// OpMeaning returns the one-line gloss.
func OpMeaning(o Op) string { return opMeaning[o] }

func (o Op) String() string { return string(o) }

// ChangeTarget is the closed set of things a proposal may change.
//
// Closed because Phase 7 has to apply each of them, and a target it has never
// heard of is a proposal that can be approved and then cannot be applied — the
// worst moment to discover it.
type ChangeTarget string

const (
	ChangeMilestone ChangeTarget = "milestone"
	ChangeWorkItem  ChangeTarget = "work_item"
	ChangeSubTask   ChangeTarget = "sub_task"
	ChangeDiagram   ChangeTarget = "diagram"
	ChangeNode      ChangeTarget = "diagram_node"
	ChangeEdge      ChangeTarget = "diagram_edge"
)

var changeTargetMeaning = map[ChangeTarget]string{
	ChangeMilestone: "a milestone and its manager-owned fields",
	ChangeWorkItem:  "a work item and its manager-owned fields, including dependencies and entity refs",
	ChangeSubTask:   "a subtask under a work item",
	ChangeDiagram:   "a diagram itself: its name, its level",
	ChangeNode:      "a box in a diagram, named by its entity slug",
	ChangeEdge:      "an arrow in a diagram, named by from, to and relation",
}

// ChangeTargets returns every target.
func ChangeTargets() []ChangeTarget {
	return []ChangeTarget{ChangeMilestone, ChangeWorkItem, ChangeSubTask, ChangeDiagram, ChangeNode, ChangeEdge}
}

// Valid reports whether t is known.
func (t ChangeTarget) Valid() bool { _, ok := changeTargetMeaning[t]; return ok }

// ChangeTargetMeaning returns the one-line gloss.
func ChangeTargetMeaning(t ChangeTarget) string { return changeTargetMeaning[t] }

// IsDiagram reports whether the target lives in the architecture graph.
func (t ChangeTarget) IsDiagram() bool {
	switch t {
	case ChangeDiagram, ChangeNode, ChangeEdge:
		return true
	}
	return false
}

func (t ChangeTarget) String() string { return string(t) }

// ValidateChangeTarget returns nil or a teaching error.
func ValidateChangeTarget(t ChangeTarget) error {
	if t.Valid() {
		return nil
	}
	return &domain.VocabularyError{
		Field: "proposal change target",
		Value: string(t),
		Valid: domain.Glossed(ChangeTargets(), ChangeTargetMeaning),
		Hint: "A target that does not exist here is a proposal that can be approved and then not applied, " +
			"which is the worst moment to find out.",
	}
}

// Change is one typed edit inside a proposal.
type Change struct {
	ID         uuid.UUID
	ProposalID uuid.UUID
	Seq        int
	Op         Op
	Target     ChangeTarget
	TargetID   *uuid.UUID

	// Before is the state when the proposal was raised. Phase 7 compares it
	// against the current state and supersedes the whole proposal if it moved:
	// approving a change whose premise has gone is how a plan silently reverts.
	Before map[string]any
	// After is what the change would make true.
	After map[string]any
}

// Proposal is a governed change set.
type Proposal struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	ActorID   uuid.UUID
	// OnBehalfOf is the instructing user when an agent raised this. An agent
	// has that user's permissions and no more (D11).
	OnBehalfOf *uuid.UUID
	TurnID     *uuid.UUID

	Title string
	// Rationale is required. A proposal without one is a diff, and a diff is
	// not something anybody can decide about.
	Rationale string
	Target    TargetKind
	Status    Status

	DecidedBy         *uuid.UUID
	DecidedAt         *time.Time
	DecisionRationale string
	AppliedRevision   *int
	CreatedAt         time.Time

	Changes []Change

	// RaisedByName and DecidedByName are filled on read.
	RaisedByName  string
	DecidedByName string
}

// Validate checks a proposal's invariants before it is written.
func Validate(p *Proposal) error {
	if strings.TrimSpace(p.Title) == "" {
		return fmt.Errorf("%w: a proposal needs a title saying what it would do", domain.ErrInvalid)
	}
	if strings.TrimSpace(p.Rationale) == "" {
		return fmt.Errorf("%w: a proposal needs a rationale. Without one it is a diff, and nobody can decide "+
			"about a diff — including the person who has to live with it", domain.ErrInvalid)
	}
	if !p.Target.Valid() {
		return &domain.VocabularyError{
			Field: "proposal target kind",
			Value: string(p.Target),
			Valid: domain.Glossed(TargetKinds(), TargetMeaning),
		}
	}
	if len(p.Changes) == 0 {
		return fmt.Errorf("%w: a proposal with no changes proposes nothing. If the answer is that nothing should "+
			"change, say so in the conversation rather than raising an empty proposal", domain.ErrInvalid)
	}
	for i, c := range p.Changes {
		if err := ValidateChangeTarget(c.Target); err != nil {
			return err
		}
		if !c.Op.Valid() {
			return &domain.VocabularyError{
				Field: "proposal change op",
				Value: string(c.Op),
				Valid: domain.Glossed(Ops(), OpMeaning),
			}
		}
		if c.Op != OpCreate && c.TargetID == nil {
			return fmt.Errorf("%w: change %d is an %s with no target; say which %s it changes",
				domain.ErrInvalid, i+1, c.Op, c.Target)
		}
		if c.Op != OpDelete && len(c.After) == 0 {
			return fmt.Errorf("%w: change %d is an %s with nothing in `after`; say what it would make true",
				domain.ErrInvalid, i+1, c.Op)
		}
	}
	return nil
}

// DeriveTarget picks the target kind from the changes, so a caller cannot claim
// a proposal is plan-only while editing a diagram in it.
func DeriveTarget(changes []Change) TargetKind {
	var sawDiagram, sawPlan bool
	for _, c := range changes {
		if c.Target.IsDiagram() {
			sawDiagram = true
		} else {
			sawPlan = true
		}
	}
	switch {
	case sawDiagram && sawPlan:
		return TargetMixed
	case sawDiagram:
		return TargetDiagram
	default:
		return TargetPlan
	}
}
