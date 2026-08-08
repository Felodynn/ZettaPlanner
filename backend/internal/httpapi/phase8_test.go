package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/gitproj"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/gitexport"
)

// Phase 8's exit criteria, over the real HTTP surface and a real Postgres.
//
// The export tests read the document the way its reader would — as text, with
// no other context — because that is the criterion. "Decomposable by a reader
// with no other context" is a judgement a human makes on real content, and what
// a test can hold is the weaker half of it: that every part the judgement needs
// is actually in the bytes.

type exportFixture struct {
	*planFixture
	did       uuid.UUID
	item      itemView
	blocker   itemView
	entities  map[string]uuid.UUID
	diagramID uuid.UUID
}

func newExportFixture(t *testing.T) *exportFixture {
	t.Helper()
	f := &exportFixture{planFixture: newPlanFixture(t)}

	// A small architecture, so the exported slice has something to say and one
	// neighbour that is context rather than scope.
	var created struct {
		ID   uuid.UUID `json:"id"`
		Slug string    `json:"slug"`
	}
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/diagrams",
		map[string]any{"name": "Auth Context", "level": "context"}), http.StatusCreated, &created)
	f.did, f.diagramID = created.ID, created.ID

	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+f.did.String()+"/lease", nil),
		http.StatusOK, nil)
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+f.did.String()+"/revisions",
		initialState(0)), http.StatusCreated, nil)

	f.entities = map[string]uuid.UUID{}
	var list struct {
		Entities []struct {
			ID   uuid.UUID `json:"id"`
			Slug string    `json:"slug"`
		} `json:"entities"`
	}
	f.h.decode(f.mgr.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/entities", nil),
		http.StatusOK, &list)
	for _, e := range list.Entities {
		f.entities[e.Slug] = e.ID
	}

	f.blocker = f.createItem(f.mgr, map[string]any{
		"title": "Token store schema", "status": "in_progress", "due_date": "2026-08-18",
	})
	f.item = f.createItem(f.mgr, map[string]any{
		"title":               "Session token rotation",
		"intent":              "Tokens live forever today, so a leaked one is permanent.",
		"acceptance_criteria": "A rotated token invalidates its predecessor within one second.",
		"milestone_id":        f.msID.String(),
		"priority":            "high",
		"start_date":          "2026-08-10",
		"due_date":            "2026-08-20",
		"assignee_id":         f.dev.userID.String(),
	})

	// The blocker blocks the item: `blocks` is set from the blocking end.
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+f.blocker.ID.String()+"/dependencies",
		map[string]any{"blocks": []string{f.item.ID.String()}}), http.StatusOK, nil)

	// Only auth-service is referenced. token-store arrives as the far end of a
	// relation, which is the distinction the slice has to make.
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+f.item.ID.String()+"/entity-refs",
		map[string]any{"implements": []string{f.entities["auth-service"].String()}}), http.StatusOK, nil)

	return f
}

// exportSpec posts the export and returns the document as text.
func (f *exportFixture) exportSpec(c *client, path string) (string, http.Header) {
	f.h.t.Helper()
	resp := c.do(http.MethodPost, path, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		f.h.t.Fatalf("%s: HTTP %d: %s", path, resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		f.h.t.Fatal(err)
	}
	return string(body), resp.Header
}

// ── exit criterion 1: the document carries all four parts ────────────────────

func TestSpecExportCarriesIntentCriteriaConstraintsAndTheEntitySlice(t *testing.T) {
	f := newExportFixture(t)
	doc, headers := f.exportSpec(f.dev, "/api/v1/work-items/"+f.item.ID.String()+"/spec-export")

	if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q; the reader saves this as a file and hands it to somebody", ct)
	}
	if cd := headers.Get("Content-Disposition"); !strings.Contains(cd, ".spec.md") {
		t.Errorf("Content-Disposition = %q; it should name a file", cd)
	}

	for _, want := range []string{
		"Tokens live forever today",              // intent, verbatim
		"invalidates its predecessor within one", // acceptance criteria, verbatim
		"## Constraints",                         //
		"Blocked by " + f.blocker.Ref,            // a constraint that is actually recorded
		"v0 backend is due 2026-09-30",           // its own dates against the milestone's
		"## Architecture",                        //
		"```yaml",                                // the canonical slice, not a paraphrase
		"id: auth-service",                       // the entity it references
		"relation: writes_to",                    // and the relation drawn around it
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the exported spec is missing %q\n\n%s", want, doc)
		}
	}

	// token-store is the far end of a relation, not something the item points
	// at. A decomposer that cannot tell the two apart treats the neighbour as
	// in scope.
	if !strings.Contains(doc, "referenced: false") {
		t.Errorf("nothing in the slice separates the item's scope from its surroundings\n\n%s", doc)
	}
}

func TestSpecExportRefusesANonMember(t *testing.T) {
	f := newExportFixture(t)
	outsider := f.admin.createUser("outsider@plan.test", "Outsider", "developer")

	resp := outsider.do(http.MethodPost, "/api/v1/work-items/"+f.item.ID.String()+"/spec-export", nil)
	defer resp.Body.Close()
	// not_found rather than forbidden, for the same reason every project-scoped
	// read answers that way: the alternative lets anyone enumerate what exists.
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a non-member exporting somebody else's work item got HTTP %d, want 404", resp.StatusCode)
	}
}

func TestSpecExportIsRecordedAsHavingHappened(t *testing.T) {
	f := newExportFixture(t)
	f.exportSpec(f.dev, "/api/v1/work-items/"+f.item.ID.String()+"/spec-export")

	var seen bool
	rows, err := f.h.db.Pool.Query(context.Background(),
		`SELECT after->>'ref' FROM audit_log WHERE project_id = $1 AND action = 'spec.exported'`, f.pid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			t.Fatal(err)
		}
		if ref == f.item.Ref {
			seen = true
		}
	}
	if !seen {
		// "Who took this outside and when" is exactly the question asked about a
		// handoff that went wrong, and it is unanswerable later if nobody wrote
		// it down at the time.
		t.Fatal("exporting a spec recorded nothing in the audit log")
	}
}

func TestMilestoneSpecCarriesItsItemsAndTheirArchitecture(t *testing.T) {
	f := newExportFixture(t)
	doc, _ := f.exportSpec(f.mgr, "/api/v1/milestones/"+f.msID.String()+"/spec-export")

	for _, want := range []string{
		"# v0 backend",
		"the substrate works end to end",
		"### " + f.item.Ref,
		"Tokens live forever today",
		"## Architecture",
		"id: auth-service",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the milestone spec is missing %q\n\n%s", want, doc)
		}
	}
	// The blocker is not in this milestone, so it is somebody else's problem
	// and belongs in the constraints rather than in the item list.
	if !strings.Contains(doc, f.blocker.Ref+", which is outside this milestone") {
		t.Errorf("a dependency that leaves the milestone is not named as a constraint\n\n%s", doc)
	}
}

// ── exit criteria 3 and 4: the projection ────────────────────────────────────

func requireGitBinary(t *testing.T) {
	t.Helper()
	if err := gitproj.Available(); err != nil {
		t.Skip("git is not on PATH; skipping the projection tests")
	}
}

func TestGitProjectionIsRegenerableToAByteIdenticalTree(t *testing.T) {
	requireGitBinary(t)
	f := newExportFixture(t)

	root := t.TempDir()
	svc := gitexport.New(f.h.db, slog.New(slog.DiscardHandler), root, 0)

	first, err := svc.Sync(context.Background(), f.pid, true)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if !first.Changed {
		t.Fatal("the first projection of a project committed nothing")
	}

	// One file per node and one per edge, at slug-derived paths (§6.2).
	for _, rel := range []string{
		"project.yaml",
		"entities/auth-service.yaml",
		"diagrams/auth-context/diagram.yaml",
		"diagrams/auth-context/nodes/auth-service.yaml",
		"diagrams/auth-context/edges/auth-service__writes_to__token-store.yaml",
	} {
		if _, err := os.Stat(filepath.Join(first.Dir, rel)); err != nil {
			t.Errorf("the projected tree has no %s: %v", rel, err)
		}
	}

	// The whole safety argument is that the tree is disposable.
	entries, _ := os.ReadDir(first.Dir)
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(first.Dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}

	second, err := svc.Sync(context.Background(), f.pid, true)
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if second.Tree != first.Tree {
		t.Fatalf("a projection rebuilt from scratch is not the same tree:\n  before %s\n  after  %s",
			first.Tree, second.Tree)
	}
}

func TestTheProjectionCarriesNoCoordinates(t *testing.T) {
	requireGitBinary(t)
	f := newExportFixture(t)

	// Store some coordinates first, so "no coordinates" is a fact about the
	// projection rather than about an empty layout table.
	var nodeID uuid.UUID
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT n.id FROM diagram_nodes n JOIN catalog_entities e ON e.id = n.entity_id
		  WHERE n.diagram_id = $1 AND e.slug = 'auth-service'`, f.did).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/diagrams/"+f.did.String()+"/layout",
		map[string]any{"nodes": []map[string]any{
			{"node_id": nodeID.String(), "x": 120.5, "y": 240.25},
		}}), http.StatusOK, nil)

	root := t.TempDir()
	svc := gitexport.New(f.h.db, slog.New(slog.DiscardHandler), root, 0)
	rep, err := svc.Sync(context.Background(), f.pid, true)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	err = filepath.Walk(rep.Dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || strings.Contains(path, "/.git/") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, banned := range []string{"120.5", "240.25", "\nx:", "\ny:"} {
			if strings.Contains(string(body), banned) {
				t.Errorf("%s contains %q; the serializer's only input type has no coordinate field (D5)",
					path, banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGitFailureNeverBlocksADatabaseWrite(t *testing.T) {
	requireGitBinary(t)
	f := newExportFixture(t)

	// A directory the projection must refuse: it has content it did not write,
	// and a sync deletes everything not under .git.
	root := t.TempDir()
	occupied := filepath.Join(root, "ZP")
	if err := os.MkdirAll(occupied, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := gitexport.New(f.h.db, slog.New(slog.DiscardHandler), root, 0)
	if _, err := svc.Sync(context.Background(), f.pid, true); err == nil {
		t.Fatal("the projection agreed to write into a directory full of somebody else's files")
	}

	// SyncAll steps over the failure rather than propagating it, and the
	// database is untouched either way — there is no code path where a write is
	// waiting on git.
	f.h.decode(f.admin.do(http.MethodPatch, "/api/v1/projects/"+f.pid.String(),
		map[string]any{"git_enabled": true}), http.StatusOK, nil)
	svc.SyncAll(context.Background())

	after := f.createItem(f.mgr, map[string]any{"title": "Written while git was broken"})
	if after.Ref == "" {
		t.Fatal("a write failed while the projection was misconfigured")
	}
	if _, err := os.Stat(filepath.Join(occupied, "main.go")); err != nil {
		t.Fatalf("the projection deleted a file in a directory it does not own: %v", err)
	}
}

// ── exit criterion 5: the audit query ────────────────────────────────────────

func TestAuditQueryReconstructsWhoChangedWhat(t *testing.T) {
	f := newExportFixture(t)

	f.h.decode(f.dev.do(http.MethodPatch, "/api/v1/work-items/"+f.item.ID.String(),
		map[string]any{"status": "in_progress"}), http.StatusOK, nil)

	entries := f.auditEntries(f.mgr, "?subject=work_item&subject_id="+f.item.ID.String())
	if len(entries) == 0 {
		t.Fatal("the audit query returned nothing for an item that was just created and moved")
	}

	var transition map[string]any
	for _, e := range entries {
		if e["action"] == "work_item.status_changed" {
			transition = e
		}
	}
	if transition == nil {
		// A transition recorded as a generic update is a transition the export
		// cannot see (§6.5 constraint 2).
		t.Fatalf("the status move is not its own action:\n%v", entries)
	}
	if transition["is_transition"] != true {
		t.Errorf("work_item.status_changed is not flagged as a transition")
	}
	if transition["actor_name"] != "Dev" {
		t.Errorf("actor_name = %v, want Dev — a uuid answers \"who\" the way a serial number answers "+
			"\"who is on the phone\"", transition["actor_name"])
	}
	if transition["request_id"] == nil || transition["request_id"] == "" {
		t.Error("the entry has no request_id; it is what groups one operation (§6.5 constraint 3)")
	}
}

func TestTheAuditQueryIsOpenToEveryMemberAndClosedToOutsiders(t *testing.T) {
	f := newExportFixture(t)

	// A developer reads it, not only a manager: a record the people it
	// describes cannot read is not a control (D63).
	if got := f.auditEntries(f.dev, ""); len(got) == 0 {
		t.Fatal("a developer reading their own project's audit log got nothing")
	}

	outsider := f.admin.createUser("nosy@plan.test", "Nosy", "developer")
	resp := outsider.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/audit", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a non-member reading the audit log got HTTP %d, want 404", resp.StatusCode)
	}
}

func (f *exportFixture) auditEntries(c *client, query string) []map[string]any {
	f.h.t.Helper()
	var out struct {
		Entries []map[string]any `json:"entries"`
	}
	f.h.decode(c.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/audit"+query, nil),
		http.StatusOK, &out)
	return out.Entries
}

// ── the dashboard ────────────────────────────────────────────────────────────

func TestDashboardCountsWhatIsActuallyOpen(t *testing.T) {
	f := newExportFixture(t)

	// Created with the status rather than patched into it: `status` is
	// developer-owned and scoped to the assignee (§4.2), so a manager cannot
	// move somebody else's item into `dropped` afterwards — which is the rule
	// working, not an obstacle to route around.
	f.createItem(f.mgr, map[string]any{"title": "Not doing this after all", "status": "dropped"})
	blocked := f.createItem(f.mgr, map[string]any{"title": "Waiting on the schema", "status": "blocked"})
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+f.blocker.ID.String()+"/dependencies",
		map[string]any{"blocks": []string{f.item.ID.String(), blocked.ID.String()}}), http.StatusOK, nil)

	var body struct {
		Totals struct {
			Items         int            `json:"work_items"`
			Open          int            `json:"open_items"`
			ByStatus      map[string]int `json:"by_status"`
			Untriaged     int            `json:"untriaged_open"`
			PendingReview int            `json:"pending_proposals"`
		} `json:"totals"`
		Milestones []struct {
			Name string `json:"name"`
			Open int    `json:"open_count"`
		} `json:"milestones"`
		Assignees []struct {
			UserID string `json:"user_id"`
			Name   string `json:"name"`
			Open   int    `json:"open"`
		} `json:"assignees"`
		Blocked []struct {
			Ref       string   `json:"ref"`
			BlockedBy []string `json:"blocked_by"`
		} `json:"blocked"`
		HorizonDays int `json:"horizon_days"`
	}
	f.h.decode(f.dev.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/dashboard", nil),
		http.StatusOK, &body)

	if body.Totals.Items != 4 {
		t.Errorf("work_items = %d, want 4", body.Totals.Items)
	}
	// `dropped` is not a delete, and it is not open either: the decision not to
	// do something is as final as doing it.
	if body.Totals.Open != 3 {
		t.Errorf("open_items = %d, want 3 — the dropped item is not open", body.Totals.Open)
	}
	if body.Totals.ByStatus["dropped"] != 1 {
		t.Errorf("by_status = %v; the dropped item should still be counted, just not as open", body.Totals.ByStatus)
	}
	// An unset priority is not `normal` (§3.5a): only the item created with
	// `high` has been triaged.
	if body.Totals.Untriaged != 2 {
		t.Errorf("untriaged_open = %d, want 2", body.Totals.Untriaged)
	}

	var unassigned, devLoad int
	for _, a := range body.Assignees {
		if a.UserID == "" {
			unassigned = a.Open
			continue
		}
		if a.Name == "Dev" {
			devLoad = a.Open
		}
	}
	if devLoad != 1 {
		t.Errorf("the dev's open load = %d, want 1", devLoad)
	}
	if unassigned != 2 {
		t.Errorf("unassigned open = %d, want 2 — the single most actionable number on the page", unassigned)
	}

	if len(body.Blocked) != 1 || body.Blocked[0].Ref != blocked.Ref {
		t.Fatalf("blocked = %v, want just %s", body.Blocked, blocked.Ref)
	}
	if len(body.Blocked[0].BlockedBy) != 1 || body.Blocked[0].BlockedBy[0] != f.blocker.Ref {
		t.Errorf("blocked_by = %v, want [%s]", body.Blocked[0].BlockedBy, f.blocker.Ref)
	}
	if body.HorizonDays != 14 {
		t.Errorf("horizon_days = %d, want the 14-day default", body.HorizonDays)
	}
}

func TestDashboardIsClosedToOutsiders(t *testing.T) {
	f := newExportFixture(t)
	outsider := f.admin.createUser("stranger@plan.test", "Stranger", "developer")

	resp := outsider.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/dashboard", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a non-member reading the dashboard got HTTP %d, want 404", resp.StatusCode)
	}
}

// ── the published ownership table ────────────────────────────────────────────

func TestTheOwnershipTableIsPublishedAsTheWritePathEnforcesIt(t *testing.T) {
	f := newExportFixture(t)

	var body struct {
		EffectiveRole string `json:"effective_role"`
		Objects       []struct {
			Kind   string `json:"kind"`
			Fields []struct {
				Field             string `json:"field"`
				Owner             string `json:"owner"`
				Scope             string `json:"scope"`
				WritableByRole    bool   `json:"writable_by_role"`
				NeedsRelationship bool   `json:"needs_relationship"`
			} `json:"fields"`
		} `json:"objects"`
	}
	f.h.decode(f.dev.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/field-ownership", nil),
		http.StatusOK, &body)

	if body.EffectiveRole != "developer" {
		t.Fatalf("effective_role = %q, want developer", body.EffectiveRole)
	}

	rules := map[string]struct {
		owner, scope            string
		writable, needsRelation bool
	}{}
	for _, o := range body.Objects {
		if o.Kind != "work_item" {
			continue
		}
		for _, r := range o.Fields {
			rules[r.Field] = struct {
				owner, scope            string
				writable, needsRelation bool
			}{r.Owner, r.Scope, r.WritableByRole, r.NeedsRelationship}
		}
	}
	if len(rules) == 0 {
		t.Fatal("the published table has no rules for work_item")
	}

	// The two halves of §4.2 that a UI has to render differently: a field the
	// role cannot own at all (the route is a proposal), and one it owns only on
	// its own rows (the answer depends on the item).
	if r := rules["due_date"]; r.owner != "manager" || r.writable {
		t.Errorf("due_date = %+v; it is manager-owned and a developer cannot write it", r)
	}
	if r := rules["status"]; r.owner != "developer" || !r.writable || !r.needsRelation {
		t.Errorf("status = %+v; it is developer-owned but only on items assigned to them", r)
	}

	// And the published answer has to match what the write path actually does,
	// or the table is decoration.
	resp := f.dev.do(http.MethodPatch, "/api/v1/work-items/"+f.item.ID.String(),
		map[string]any{"due_date": "2026-09-09"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("the table says a developer cannot set due_date, and the write path returned %d",
			resp.StatusCode)
	}
}
