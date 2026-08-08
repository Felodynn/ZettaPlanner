package spec_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Felodynn/ZettaPlanner/backend/internal/spec"
)

func exportedAt() time.Time { return time.Date(2026, 8, 8, 9, 30, 0, 0, time.UTC) }

func fullItem() spec.Export {
	return spec.Export{
		Kind:       spec.KindWorkItem,
		Project:    spec.Project{Key: "ZP", Name: "ZettaPlanner v0", Summary: "The planning substrate itself."},
		ExportedAt: exportedAt(),
		ExportedBy: "Sunil",
		Item: &spec.Item{
			Ref:         "ZP-14",
			Title:       "Session token rotation",
			Intent:      "Tokens live forever today, so a leaked one is permanent.",
			Acceptance:  "A rotated token invalidates its predecessor within one second.",
			Status:      "in_progress",
			Priority:    "high",
			ProgressPct: 45,
			Assignee:    "Sunil",
			StartDate:   "2026-08-10",
			DueDate:     "2026-08-20",
			Milestone:   "M1: Beta",
			MilestoneDu: "2026-09-01",
			SubTasks:    []spec.SubTask{{Title: "Write the rotation endpoint", Status: "todo"}},
			Checklist:   []spec.Check{{Text: "Rotation is audited", Checked: true}},
			BlockedBy: []spec.Link{
				{Ref: "ZP-3", Title: "Token store schema", Status: "in_progress", DueDate: "2026-08-18"},
				{Ref: "ZP-4", Title: "Old thing", Status: "done"},
			},
			Blocks:      []spec.Link{{Ref: "ZP-20", Title: "Session dashboard", Status: "todo"}},
			Deviations:  []spec.Deviation{{Summary: "The store rejects concurrent rotations", Severity: "high"}},
			EntitySlugs: []string{"auth-service", "token-store"},
		},
		Architecture: []byte("entities:\n  - id: auth-service\n    kind: service\n"),
		Notes: []spec.Note{{Kind: "overlap", Subject: "token-store",
			Body: "May be the same store as session-db.\nUnresolved."}},
		Unverified: []spec.Unverified{{Slug: "token-store", Name: "Token Store"}},
	}
}

func TestAWorkItemSpecCarriesIntentCriteriaConstraintsAndTheSlice(t *testing.T) {
	body := string(spec.Render(fullItem()))

	for _, want := range []string{
		"# ZP-14 — Session token rotation",
		"## Intent",
		"Tokens live forever today",
		"## Acceptance criteria",
		"A rotated token invalidates its predecessor",
		"- [x] Rotation is audited",
		"## Constraints",
		"## Architecture",
		"```yaml",
		"id: auth-service",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the exported spec is missing %q — §7.1's four required parts are intent, criteria, "+
				"constraints and the entity slice:\n\n%s", want, body)
		}
	}
}

func TestConstraintsAreDerivedFromWhatIsActuallyRecorded(t *testing.T) {
	body := string(spec.Render(fullItem()))
	constraints := sectionOf(t, body, "## Constraints")

	for _, want := range []string{
		"Blocked by ZP-3",       // an open blocker
		"ZP-20 is waiting",      // the other direction, which bounds the interface
		"Runs 2026-08-10",       // its own dates
		"M1: Beta is due",       // against the milestone's
		"Open deviation (high)", // reality has already diverged
		"Unresolved overlap",    // an open question about the architecture
		"never confirmed",       // an entity a vision model read and nobody checked
	} {
		if !strings.Contains(constraints, want) {
			t.Errorf("the constraints section does not mention %q:\n\n%s", want, constraints)
		}
	}
	// A finished blocker is not blocking anything, and listing it teaches the
	// reader to skim the section.
	if strings.Contains(constraints, "ZP-4") {
		t.Errorf("a blocker that is already done was listed as a constraint:\n\n%s", constraints)
	}
}

func TestAnEmptyFieldIsStatedRatherThanOmitted(t *testing.T) {
	e := spec.Export{
		Kind:       spec.KindWorkItem,
		Project:    spec.Project{Key: "ZP"},
		ExportedAt: exportedAt(),
		Item:       &spec.Item{Ref: "ZP-1", Title: "Something", Status: "todo"},
	}
	body := string(spec.Render(e))

	// A missing heading reads as a bug in the export. An explicit "none
	// recorded" reads as a fact about the plan, which is what it is — and, for
	// acceptance criteria, an important one.
	for _, want := range []string{
		"## Intent",
		"No intent was recorded",
		"## Acceptance criteria",
		"*None recorded.*",
		"## Constraints",
		"## Architecture",
		"No architecture is referenced",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("an empty item's spec is missing %q:\n\n%s", want, body)
		}
	}
}

func TestTheDocumentSaysItIsASnapshotAndOneWay(t *testing.T) {
	body := string(spec.Render(fullItem()))
	if !strings.Contains(body, "snapshot") || !strings.Contains(body, "reads it back in") {
		t.Fatalf("nothing tells the reader the plan may have moved and that edits here reach nobody:\n\n%s", body)
	}
}

func TestAMilestoneSpecNamesOnlyDependenciesThatLeaveIt(t *testing.T) {
	e := spec.Export{
		Kind:       spec.KindMilestone,
		Project:    spec.Project{Key: "ZP"},
		ExportedAt: exportedAt(),
		Milestone: &spec.Milestone{
			Name: "M1: Beta", Status: "active", DueDate: "2026-09-01",
			Items: []spec.Item{
				{Ref: "ZP-14", Title: "Rotation", Status: "todo",
					BlockedBy: []spec.Link{
						{Ref: "ZP-3", Title: "Inside the milestone", Status: "todo"},
						{Ref: "ZP-99", Title: "Somebody else's problem", Status: "in_progress"},
					}},
				{Ref: "ZP-3", Title: "Inside the milestone", Status: "todo"},
			},
		},
	}
	body := string(spec.Render(e))
	constraints := sectionOf(t, body, "## Constraints")

	if !strings.Contains(constraints, "ZP-99") {
		t.Errorf("a dependency outside the milestone is the one that is somebody else's, and it is "+
			"not in the constraints:\n\n%s", constraints)
	}
	if strings.Contains(constraints, "ZP-3, which is outside") {
		t.Errorf("a blocker inside the milestone is already in the item list above; repeating it "+
			"buries the ones that are not:\n\n%s", constraints)
	}
	if !strings.Contains(body, "### ZP-14 — Rotation") {
		t.Errorf("the milestone spec does not carry its work items:\n\n%s", body)
	}
}

func TestFilenameIsDerivedFromTheSubject(t *testing.T) {
	if got := spec.Filename(fullItem()); got != "zp-14.spec.md" {
		t.Errorf("work item filename = %q, want zp-14.spec.md", got)
	}
	m := spec.Export{Kind: spec.KindMilestone, Milestone: &spec.Milestone{Name: "M1: Beta"}}
	if got := spec.Filename(m); got != "m1-beta.spec.md" {
		t.Errorf("milestone filename = %q, want m1-beta.spec.md", got)
	}
}

// sectionOf returns the text between a heading and the next one.
func sectionOf(t *testing.T, body, heading string) string {
	t.Helper()
	i := strings.Index(body, heading)
	if i < 0 {
		t.Fatalf("no %q section:\n\n%s", heading, body)
	}
	rest := body[i+len(heading):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		return rest[:j]
	}
	return rest
}
