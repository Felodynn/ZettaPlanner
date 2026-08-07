package audit

import (
	"strings"
	"testing"
)

// TestTransitionsAreNotGenericUpdates is the Phase 0 exit criterion "transitions
// distinct from *.updated" (CONTRACT §6.5 constraint 2), asserted rather than
// intended.
//
// Bottleneck analysis is almost entirely transition analysis. A transition
// recorded as a generic update is a transition the v0.1 export cannot see, and
// the export cannot be backfilled.
func TestTransitionsAreNotGenericUpdates(t *testing.T) {
	for _, a := range Transitions() {
		if strings.HasSuffix(string(a), ".updated") {
			t.Errorf("%q is registered as a transition but is named as a generic update; give it its own verb", a)
		}
		if !a.Valid() {
			t.Errorf("%q is a transition but is not in the known set", a)
		}
	}
}

// TestStatusChangesAreTransitions pins the specific actions the contract names.
// If one of these ever stops being a transition, the export silently loses a
// dimension.
func TestStatusChangesAreTransitions(t *testing.T) {
	required := []Action{
		ActionWorkItemStatusChanged,
		ActionMilestoneStatusChanged,
		ActionSubTaskStatusChanged,
		ActionProposalDecided,
		ActionDeviationResolved,
		ActionChecklistChecked,
		ActionDiagramRevised,
		ActionIngestAccepted,
		ActionTurnEnded,
	}
	for _, a := range required {
		if !a.IsTransition() {
			t.Errorf("%q is named in CONTRACT §6.5 as a transition but is not registered as one", a)
		}
	}
}

func TestSubjectKindDerivedFromAction(t *testing.T) {
	cases := map[Action]string{
		ActionWorkItemStatusChanged: "work_item",
		ActionProposalDecided:       "proposal",
		ActionTurnEnded:             "turn",
		ActionChecklistChecked:      "checklist",
		ActionEntityCreated:         "catalog_entity",
	}
	for a, want := range cases {
		if got := a.SubjectKind(); got != want {
			t.Errorf("%q.SubjectKind() = %q, want %q", a, got, want)
		}
	}
}

// TestEveryActionIsDotted keeps the taxonomy shaped so that SubjectKind is
// meaningful and so the export can group by subject.
func TestEveryActionIsDotted(t *testing.T) {
	for _, a := range Actions() {
		kind, verb, ok := strings.Cut(string(a), ".")
		if !ok || kind == "" || verb == "" {
			t.Errorf("action %q must be shaped subject_kind.verb", a)
		}
		if strings.ToLower(string(a)) != string(a) {
			t.Errorf("action %q must be lower case", a)
		}
	}
}

func TestUnregisteredActionIsInvalid(t *testing.T) {
	// The point of the enum is that a raw string cannot reach the log. The type
	// conversion compiles; Valid is what stops it, and Write refuses on it.
	if Action("work_item.vibes_changed").Valid() {
		t.Error("an unregistered action must not validate")
	}
}
