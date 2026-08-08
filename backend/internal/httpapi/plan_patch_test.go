package httpapi

import (
	"testing"

	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
)

// TestPlanPatchBodiesAreFullyMapped is the mechanical half of "field ownership
// enforced per field".
//
// A field a request can set but CONTRACT §4.2 has never heard of is a field
// written with no owner checked — and it fails open, because CheckWrite only
// ever sees the names the body carried. The equivalent test in internal/policy
// uses synthetic structs; this one uses the structs the server actually decodes
// into, which is where the drift would happen.
func TestPlanPatchBodiesAreFullyMapped(t *testing.T) {
	cases := []struct {
		kind  policy.ObjectKind
		patch any
	}{
		{policy.KindMilestone, milestonePatchBody{}},
		{policy.KindWorkItem, workItemPatchBody{}},
		{policy.KindSubTask, subTaskPatchBody{}},
		{policy.KindChecklistItem, checklistPatchBody{}},
		{policy.KindDeviation, deviationPatchBody{}},
		{policy.KindProject, projectPatchBody{}},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			if unmapped := policy.UnmappedFields(tc.kind, tc.patch); len(unmapped) != 0 {
				t.Errorf("these fields can be set but have no rule in the ownership table: %v.\n"+
					"Add them to internal/policy/ownership.go and to CONTRACT §4.2, or take them off the wire.",
					unmapped)
			}
		})
	}
}

// TestEveryPatchFieldIsReachable is the same check from the other side: a rule
// in the table that no request can exercise is a rule nobody is enforcing,
// which reads in review as though somebody is.
//
// `resolved_at` is the deliberate exception. It is derived from the resolution
// rather than written directly: two fields that must agree are two fields that
// eventually will not, and the one that would be wrong is the timestamp the
// retrospective reads.
func TestEveryPatchFieldIsReachable(t *testing.T) {
	reachable := map[policy.ObjectKind]map[string]bool{
		policy.KindMilestone:     asSet(policy.PatchFieldNames(milestonePatchBody{})),
		policy.KindWorkItem:      asSet(policy.PatchFieldNames(workItemPatchBody{})),
		policy.KindSubTask:       asSet(policy.PatchFieldNames(subTaskPatchBody{})),
		policy.KindChecklistItem: asSet(policy.PatchFieldNames(checklistPatchBody{})),
		policy.KindDeviation:     asSet(policy.PatchFieldNames(deviationPatchBody{})),
	}
	// Written through their own endpoints rather than a patch body, because
	// each replaces a whole set.
	reachable[policy.KindWorkItem]["dependencies"] = true
	reachable[policy.KindWorkItem]["entity_refs"] = true
	// Derived, on purpose.
	reachable[policy.KindDeviation]["resolved_at"] = true

	for kind, fields := range reachable {
		rules, _, ok := policy.Rules(kind)
		if !ok {
			t.Fatalf("%s is not in the ownership table", kind)
		}
		for field := range rules {
			if !fields[field] {
				t.Errorf("%s.%s has an ownership rule but nothing can write it. Either the endpoint is missing "+
					"or the rule is dead — and a dead rule reads in review as an enforced one.", kind, field)
			}
		}
	}
}

func asSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}
