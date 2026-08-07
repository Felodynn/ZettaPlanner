package policy

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func dev() Subject {
	return Subject{
		Principal:     Principal{UserID: uuid.Must(uuid.NewV7()), GlobalRole: RoleDeveloper},
		EffectiveRole: RoleDeveloper,
		IsMember:      true,
	}
}

func manager() Subject {
	s := dev()
	s.GlobalRole = RoleManager
	s.EffectiveRole = RoleManager
	return s
}

func admin() Subject {
	s := dev()
	s.GlobalRole = RoleAdmin
	s.EffectiveRole = RoleAdmin
	return s
}

// TestOwnershipMatrix pins the specific cells of CONTRACT §4.2. A wrong entry in
// the table is the kind of mistake that looks right in review and only shows up
// as a permission someone quietly had all along.
func TestOwnershipMatrix(t *testing.T) {
	assigned := func(s Subject) Subject { s.IsAssignee = true; return s }
	author := func(s Subject) Subject { s.IsAuthor = true; return s }
	reporter := func(s Subject) Subject { s.IsReporter = true; return s }

	cases := []struct {
		name    string
		kind    ObjectKind
		field   string
		subject Subject
		allow   bool
	}{
		// WorkItem: manager-owned
		{"manager sets a due date", KindWorkItem, "due_date", manager(), true},
		{"developer sets a due date", KindWorkItem, "due_date", assigned(dev()), false},
		{"developer sets the assignee", KindWorkItem, "assignee_id", assigned(dev()), false},
		{"developer sets acceptance criteria", KindWorkItem, "acceptance_criteria", assigned(dev()), false},

		// WorkItem: dev-owned, scoped to the assignee
		{"assignee sets status", KindWorkItem, "status", assigned(dev()), true},
		{"non-assignee sets status", KindWorkItem, "status", dev(), false},
		{"assignee sets progress", KindWorkItem, "progress_pct", assigned(dev()), true},

		// Not a ladder. A manager does not acquire a dev-owned field by
		// outranking its owner: status is the developer's report of reality,
		// and the manager's route is to reassign, which is manager-owned.
		{"manager sets status", KindWorkItem, "status", assigned(manager()), false},
		{"manager sets progress", KindWorkItem, "progress_pct", manager(), false},

		// Milestone: entirely manager-owned
		{"manager sets a milestone date", KindMilestone, "due_date", manager(), true},
		{"developer sets a milestone status", KindMilestone, "status", assigned(dev()), false},

		// SubTask and ChecklistItem: dev-owned within the caller's own item
		{"assignee retitles a subtask", KindSubTask, "title", assigned(dev()), true},
		{"non-assignee retitles a subtask", KindSubTask, "title", dev(), false},
		{"assignee ticks a checklist item", KindChecklistItem, "checked", assigned(dev()), true},
		{"non-assignee ticks a checklist item", KindChecklistItem, "checked", dev(), false},

		// Worklog: author only
		{"author edits their worklog", KindWorklog, "body", author(dev()), true},
		{"someone else edits a worklog", KindWorklog, "body", dev(), false},

		// Deviation: split between reporter and manager
		{"reporter sets severity", KindDeviation, "severity", reporter(dev()), true},
		{"non-reporter sets severity", KindDeviation, "severity", dev(), false},
		{"manager resolves a deviation", KindDeviation, "resolution", manager(), true},
		{"reporter resolves a deviation", KindDeviation, "resolution", reporter(dev()), false},

		// Diagram and project settings: all manager-owned
		{"manager edits a diagram", KindDiagram, "anything_at_all", manager(), true},
		{"developer edits a diagram", KindDiagram, "anything_at_all", assigned(dev()), false},
		{"manager edits project settings", KindProject, "settings", manager(), true},
		{"developer edits project settings", KindProject, "settings", dev(), false},

		// Admin is above the table entirely (§4.1).
		{"admin sets a due date", KindWorkItem, "due_date", admin(), true},
		{"admin sets status", KindWorkItem, "status", admin(), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := CheckWrite(tc.kind, []string{tc.field}, tc.subject)
			if tc.allow && d != nil {
				t.Fatalf("expected the write to be allowed, got: %s", d.Message)
			}
			if !tc.allow && d == nil {
				t.Fatalf("expected %s.%s to be denied for %s", tc.kind, tc.field, tc.subject.EffectiveRole)
			}
		})
	}
}

// TestCheckIsDerivedFromTheTable is the mechanical property behind the exit
// criterion "adding a field to the table changes behaviour with no handler
// change".
//
// It is generated from the table rather than written out, so a field added
// tomorrow is covered by this test tomorrow, without anybody remembering to
// extend it. If CheckWrite ever grew a special case for a particular field, this
// is what would catch it.
func TestCheckIsDerivedFromTheTable(t *testing.T) {
	for _, kind := range Kinds() {
		fields, all, _ := Rules(kind)
		if all != nil {
			continue // wildcard objects are covered by the matrix above
		}
		for field, rule := range fields {
			t.Run(string(kind)+"."+field, func(t *testing.T) {
				// The owner named by the table, with every relationship flag
				// satisfied, must always be allowed.
				owner := dev()
				switch rule.Owner {
				case OwnerManager:
					owner = manager()
				case OwnerAdmin:
					owner = admin()
				}
				owner.IsAssignee, owner.IsAuthor, owner.IsReporter = true, true, true
				if d := CheckWrite(kind, []string{field}, owner); d != nil {
					t.Errorf("the table says %s owns %s.%s, but the check denied it: %s",
						rule.Owner, kind, field, d.Message)
				}

				// The other role must always be denied. Ownership is two
				// columns, not two tiers.
				other := manager()
				if rule.Owner == OwnerManager {
					other = dev()
					other.IsAssignee, other.IsAuthor, other.IsReporter = true, true, true
				}
				if d := CheckWrite(kind, []string{field}, other); d == nil {
					t.Errorf("%s.%s is %s-owned but %s was allowed to write it",
						kind, field, rule.Owner, other.EffectiveRole)
				}

				// A scoped rule must deny the right role without the
				// relationship.
				if rule.Scope != ScopeProject && rule.Owner == OwnerDeveloper {
					bare := dev()
					if d := CheckWrite(kind, []string{field}, bare); d == nil {
						t.Errorf("%s.%s is scoped %s but a developer with no relationship was allowed",
							kind, field, rule.Scope)
					}
				}
			})
		}
	}
}

// TestDenialNamesRoleAndRoute is CONTRACT §7.3: a denial's wording is a prompt
// layer. Lead with the action.
func TestDenialLeadsWithTheRoute(t *testing.T) {
	d := CheckWrite(KindWorkItem, []string{"due_date"}, dev())
	if d == nil {
		t.Fatal("expected a denial")
	}
	if !strings.HasPrefix(d.Message, "To change due_date, raise a proposal") {
		t.Errorf("denial does not lead with the route forward: %q", d.Message)
	}
	for _, want := range []string{"manager-owned", "developer"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("denial is missing %q: %q", want, d.Message)
		}
	}
	details := d.Details()
	if details["owning_role"] != "manager" || details["route"] != "raise a proposal" {
		t.Errorf("details do not carry the role and the route: %v", details)
	}
}

// TestUnknownFieldTeaches: the error is read by the agent in Phase 6 as well as
// by a human, so it lists what is valid rather than only saying no.
func TestUnknownFieldTeaches(t *testing.T) {
	d := CheckWrite(KindWorkItem, []string{"vibes"}, manager())
	if d == nil {
		t.Fatal("expected an unknown field to be rejected")
	}
	if d.Kind != DenialUnknownField {
		t.Errorf("kind = %q, want %q", d.Kind, DenialUnknownField)
	}
	if !strings.Contains(d.Message, "status") || !strings.Contains(d.Message, "due_date") {
		t.Errorf("the message should list the writable fields: %q", d.Message)
	}
	if len(d.Details()["valid_fields"].([]string)) == 0 {
		t.Error("details should carry the valid field list")
	}
}

// TestCheckWriteRejectsBeforeAnyWrite: a request touching an allowed and a
// disallowed field is denied whole. Checking as each field is applied would
// leave the allowed half written behind the denial.
func TestCheckWriteIsAllOrNothing(t *testing.T) {
	s := dev()
	s.IsAssignee = true
	d := CheckWrite(KindWorkItem, []string{"status", "due_date"}, s)
	if d == nil {
		t.Fatal("a patch mixing a dev-owned and a manager-owned field must be denied whole")
	}
	if d.Field != "due_date" {
		t.Errorf("the denial names %q; it should name the field that was not allowed", d.Field)
	}
}

func TestNonMemberIsDeniedWithARoute(t *testing.T) {
	s := dev()
	s.IsMember = false
	d := CheckWrite(KindWorkItem, []string{"status"}, s)
	if d == nil {
		t.Fatal("a non-member must be denied")
	}
	if !strings.Contains(d.Message, "ask an admin to add you") {
		t.Errorf("the denial should name the route in: %q", d.Message)
	}
}

// TestWorklogsAreNeverDeleted mirrors the database trigger at the policy layer,
// so the refusal explains itself rather than surfacing as a constraint error.
func TestWorklogsAreNeverDeleted(t *testing.T) {
	d := CheckDelete(KindWorklog, admin())
	if d == nil {
		t.Fatal("worklogs must not be deletable, not even by an admin")
	}
	if !strings.Contains(d.Message, "add a new one") {
		t.Errorf("the refusal should name the correction route: %q", d.Message)
	}
}

func TestLifecycleRules(t *testing.T) {
	cases := []struct {
		name  string
		fn    func(ObjectKind, Subject) *Denial
		kind  ObjectKind
		subj  Subject
		allow bool
	}{
		{"manager creates a work item", CheckCreate, KindWorkItem, manager(), true},
		{"developer creates a work item", CheckCreate, KindWorkItem, dev(), false},
		{"manager creates a milestone", CheckCreate, KindMilestone, manager(), true},
		{"developer creates a milestone", CheckCreate, KindMilestone, dev(), false},
		// "A developer creating a SubTask under their own assigned item is
		// dev-owned" (§4.2).
		{"assignee creates a subtask", CheckCreate, KindSubTask, func() Subject { s := dev(); s.IsAssignee = true; return s }(), true},
		{"non-assignee creates a subtask", CheckCreate, KindSubTask, dev(), false},
		{"anyone on the project adds a worklog", CheckCreate, KindWorklog, dev(), true},
		{"admin creates a project", CheckCreate, KindProject, admin(), true},
		{"manager creates a project", CheckCreate, KindProject, manager(), false},
		{"manager deletes a work item", CheckDelete, KindWorkItem, manager(), true},
		{"developer deletes a work item", CheckDelete, KindWorkItem, dev(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.fn(tc.kind, tc.subj)
			if tc.allow && d != nil {
				t.Fatalf("expected allowed, got: %s", d.Message)
			}
			if !tc.allow && d == nil {
				t.Fatal("expected a denial")
			}
		})
	}
}

// ── effective role ───────────────────────────────────────────────────────────

func TestEffectiveRole(t *testing.T) {
	mgr, devRole, adm := RoleManager, RoleDeveloper, RoleAdmin

	cases := []struct {
		name     string
		global   Role
		override *Role
		want     Role
	}{
		{"no override keeps the global role", RoleDeveloper, nil, RoleDeveloper},
		{"override raises", RoleDeveloper, &mgr, RoleManager},
		// The one that matters: a manager added to a project as a developer is
		// a developer there. An override that could only raise would be a
		// permission you cannot take away.
		{"override lowers", RoleManager, &devRole, RoleDeveloper},
		{"admin is not lowered by a project role", RoleAdmin, &devRole, RoleAdmin},
		{"admin override on a developer", RoleDeveloper, &adm, RoleAdmin},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Effective(tc.global, tc.override); got != tc.want {
				t.Errorf("Effective(%s, %v) = %s, want %s", tc.global, tc.override, got, tc.want)
			}
		})
	}
}

// ── field derivation ─────────────────────────────────────────────────────────

func TestFieldsReadsOnlyWhatWasSet(t *testing.T) {
	type patch struct {
		Name     *string `json:"name,omitempty"`
		DueDate  *string `json:"due_date,omitempty"`
		Status   *string `json:"status,omitempty"`
		Ignored  string  `json:"ignored"` // not a pointer: a path or query param
		NoTag    *string
		Excluded *string `json:"-"`
	}
	name, status := "x", "in_progress"
	got := Fields(patch{Name: &name, Status: &status})

	if len(got) != 2 || got[0] != "name" || got[1] != "status" {
		t.Errorf("Fields = %v, want [name status]", got)
	}

	all := PatchFieldNames(patch{})
	if len(all) != 3 {
		t.Errorf("PatchFieldNames = %v, want the three tagged pointer fields", all)
	}
}

// TestPatchStructsAreFullyMapped: a field a request can set but the table has
// never heard of would be written with no owner checked. This is what makes
// that impossible quietly.
func TestPatchStructsAreFullyMapped(t *testing.T) {
	type workItemPatch struct {
		Title       *string `json:"title,omitempty"`
		Status      *string `json:"status,omitempty"`
		DueDate     *string `json:"due_date,omitempty"`
		ProgressPct *int    `json:"progress_pct,omitempty"`
	}
	if unmapped := UnmappedFields(KindWorkItem, workItemPatch{}); len(unmapped) != 0 {
		t.Errorf("these fields have no rule in the ownership table: %v", unmapped)
	}

	type driftedPatch struct {
		Vibes *string `json:"vibes,omitempty"`
	}
	if unmapped := UnmappedFields(KindWorkItem, driftedPatch{}); len(unmapped) != 1 {
		t.Errorf("an unmapped field should be reported, got %v", unmapped)
	}
}
