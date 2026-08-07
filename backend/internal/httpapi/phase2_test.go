package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
)

// Phase 2's exit criteria, over the real HTTP surface and a real Postgres.

// diagramFixture is a project with one admin, one manager and one developer,
// plus an empty diagram with the lease held by the admin.
type diagramFixture struct {
	h       *harness
	admin   *client
	mgr     *client
	dev     *client
	pid     uuid.UUID
	did     uuid.UUID
	diaSlug string
}

func newDiagramFixture(t *testing.T) *diagramFixture {
	t.Helper()
	h := newHarness(t)
	admin := h.bootstrapAdmin()
	mgr := admin.createUser("mgr@test.local", "Manager", policy.RoleManager)
	dev := admin.createUser("dev@test.local", "Dev", policy.RoleDeveloper)

	pid := h.createProject(admin, "ZP", "ZettaPlanner")
	for _, m := range []struct {
		c    *client
		role string
	}{{mgr, "manager"}, {dev, "developer"}} {
		h.decode(admin.do(http.MethodPut,
			"/api/v1/projects/"+pid.String()+"/members/"+m.c.userID.String(),
			map[string]any{"role": m.role}), http.StatusNoContent, nil)
	}

	var created struct {
		ID   uuid.UUID `json:"id"`
		Slug string    `json:"slug"`
	}
	h.decode(admin.do(http.MethodPost, "/api/v1/projects/"+pid.String()+"/diagrams",
		map[string]any{"name": "Auth Context", "level": "context"}), http.StatusCreated, &created)

	return &diagramFixture{h: h, admin: admin, mgr: mgr, dev: dev, pid: pid, did: created.ID, diaSlug: created.Slug}
}

func (f *diagramFixture) lease(c *client) *http.Response {
	return c.do(http.MethodPost, "/api/v1/diagrams/"+f.did.String()+"/lease", nil)
}

func (f *diagramFixture) save(c *client, body map[string]any) *http.Response {
	return c.do(http.MethodPost, "/api/v1/diagrams/"+f.did.String()+"/revisions", body)
}

// initialState is the CONTRACT §6.2 example.
func initialState(base int) map[string]any {
	return map[string]any{
		"base_revision": base,
		"summary":       "initial auth context",
		"nodes": []map[string]any{
			{"slug": "auth-service", "kind": "service", "name": "Auth Service",
				"summary": "Issues and validates session tokens", "attrs": map[string]any{"language": "go"}},
			{"slug": "token-store", "kind": "datastore", "name": "Token Store"},
			{"slug": "browser", "kind": "actor", "name": "Browser"},
		},
		"edges": []map[string]any{
			{"from": "auth-service", "to": "token-store", "relation": "writes_to", "label": "session rows"},
			{"from": "browser", "to": "auth-service", "relation": "calls", "label": "POST /auth/token"},
		},
	}
}

func (f *diagramFixture) yaml(c *client) string {
	f.h.t.Helper()
	resp := c.do(http.MethodGet, "/api/v1/diagrams/"+f.did.String()+"/yaml", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		f.h.t.Fatalf("yaml: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		f.h.t.Fatal(err)
	}
	return string(body)
}

// ── the exit criteria ────────────────────────────────────────────────────────

// TestSerializedYAMLHasNoCoordinates is the D5 guarantee, checked through the
// API after a layout write has actually stored some.
func TestSerializedYAMLHasNoCoordinates(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	// Store real coordinates first, so this is not passing merely because none
	// exist.
	var view struct {
		Nodes []struct {
			ID   uuid.UUID `json:"id"`
			Slug string    `json:"slug"`
		} `json:"nodes"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/diagrams/"+f.did.String(), nil), http.StatusOK, &view)
	if len(view.Nodes) == 0 {
		t.Fatal("no nodes to place")
	}
	f.h.decode(f.admin.do(http.MethodPut, "/api/v1/diagrams/"+f.did.String()+"/layout", map[string]any{
		"nodes": []map[string]any{{"node_id": view.Nodes[0].ID, "x": 120.5, "y": 40, "w": 180, "h": 80}},
	}), http.StatusOK, nil)

	body := f.yaml(f.admin)

	// Anchored on YAML keys: `summary:` contains "y:", and a substring match
	// would report a false positive on prose.
	forbidden := regexp.MustCompile(`(?m)^\s*-?\s*(x|y|w|h|width|height|collapsed|unplaced|waypoints|layout|position)\s*:`)
	if m := forbidden.FindString(body); m != "" {
		t.Errorf("presentation data reached the serialized diagram (%q):\n%s", strings.TrimSpace(m), body)
	}
	if !strings.Contains(body, "diagram: auth-context") {
		t.Errorf("the document does not open with the diagram slug:\n%s", body)
	}
}

// TestSerializationIsDeterministicOverHTTP: the same state, fetched twice, is
// byte-identical. An empty diff must mean nothing changed.
func TestSerializationIsDeterministicOverHTTP(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	first := f.yaml(f.admin)
	for i := 0; i < 5; i++ {
		if got := f.yaml(f.admin); got != first {
			t.Fatalf("fetch %d differs:\n%s\n---\n%s", i, first, got)
		}
	}
}

// TestLeaseIsHeldByOneEditor is the two-session race from the manual test.
func TestLeaseIsHeldByOneEditor(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)

	body := f.h.decodeErr(f.lease(f.mgr), http.StatusConflict)
	if body.Error.Code != "lease_held" {
		t.Errorf("code = %q, want lease_held", body.Error.Code)
	}
	// A refusal that names nobody leaves the caller with nothing to do.
	if body.Error.Details["holder_name"] != "Admin" {
		t.Errorf("the denial does not name the holder: %v", body.Error.Details)
	}
	if body.Error.Details["expires_at"] == nil {
		t.Error("the denial does not say when the session lapses")
	}
}

// TestDeveloperCannotTakeADiagramLease: a lease is the right to make a semantic
// change, so it needs the authority to make one. Handing a developer a lease
// they can never save under would be worse than refusing.
func TestDeveloperCannotTakeADiagramLease(t *testing.T) {
	f := newDiagramFixture(t)

	body := f.h.decodeErr(f.lease(f.dev), http.StatusForbidden)
	if body.Error.Code != "field_not_owned" {
		t.Errorf("code = %q, want field_not_owned", body.Error.Code)
	}
	if body.Error.Details["route"] != "raise a proposal" {
		t.Errorf("the denial does not name the route forward: %v", body.Error.Details)
	}
}

// TestStaleBaseIsRejectedWithTheDiff — never merged.
func TestStaleBaseIsRejectedWithTheDiff(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	// Save again from the revision we already left behind.
	body := f.h.decodeErr(f.save(f.admin, initialState(0)), http.StatusConflict)

	if body.Error.Code != "stale_base_revision" {
		t.Fatalf("code = %q, want stale_base_revision", body.Error.Code)
	}
	if body.Error.Details["current_revision"] != float64(1) {
		t.Errorf("current_revision = %v, want 1", body.Error.Details["current_revision"])
	}

	since, ok := body.Error.Details["revisions_since"].([]any)
	if !ok || len(since) != 1 {
		t.Fatalf("the rejection carries no diff: %v", body.Error.Details["revisions_since"])
	}
	rev := since[0].(map[string]any)
	changes := rev["changes"].(map[string]any)
	if added, ok := changes["nodes_added"].([]any); !ok || len(added) != 3 {
		t.Errorf("the diff does not say what changed: %v", changes)
	}

	// And nothing was merged: the state is still what the first save left.
	if strings.Count(f.yaml(f.admin), "- id: ") != 3 {
		t.Error("the rejected save altered the diagram")
	}
}

// TestExpiredLeaseBlocksTheSave is the "let a lease expire mid-edit" case.
func TestExpiredLeaseBlocksTheSave(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	// Expire it the way a laptop lid does.
	if _, err := f.h.db.Pool.Exec(context.Background(),
		`UPDATE diagram_leases SET expires_at = now() - interval '1 minute' WHERE diagram_id = $1`, f.did); err != nil {
		t.Fatal(err)
	}

	next := initialState(1)
	next["summary"] = "after expiry"
	body := f.h.decodeErr(f.save(f.admin, next), http.StatusConflict)
	if !strings.Contains(body.Error.Message, "lapsed") {
		t.Errorf("the refusal does not explain that the session lapsed: %q", body.Error.Message)
	}

	// A lapsed lease is taken over rather than blocking forever.
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, next), http.StatusCreated, nil)
}

// TestHeartbeatFailsOnceLapsed: the client should find out with the work still
// on screen, not at save time.
func TestHeartbeatFailsOnceLapsed(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)

	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/diagrams/"+f.did.String()+"/lease/heartbeat", nil),
		http.StatusOK, nil)

	if _, err := f.h.db.Pool.Exec(context.Background(),
		`UPDATE diagram_leases SET expires_at = now() - interval '1 minute' WHERE diagram_id = $1`, f.did); err != nil {
		t.Fatal(err)
	}
	body := f.h.decodeErr(f.admin.do(http.MethodPost, "/api/v1/diagrams/"+f.did.String()+"/lease/heartbeat", nil),
		http.StatusConflict)

	// It must not claim other people's saves landed when they did not — the
	// lease records the revision it was taken at, not the one the client last
	// saw, so counting from it would count the holder's own work.
	if strings.Contains(body.Error.Message, "save(s) landed") {
		t.Errorf("the heartbeat failure claims saves landed that it cannot know about: %q", body.Error.Message)
	}
}

// TestLayoutWriteProducesNoRevisionAndNeedsNoLease.
func TestLayoutWriteProducesNoRevisionAndNeedsNoLease(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	var view struct {
		LayoutMode string `json:"layout_mode"`
		Revision   int    `json:"revision"`
		Nodes      []struct {
			ID uuid.UUID `json:"id"`
		} `json:"nodes"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/diagrams/"+f.did.String(), nil), http.StatusOK, &view)
	if view.LayoutMode != "auto" {
		t.Fatalf("a new diagram starts in %q, want auto", view.LayoutMode)
	}
	revisionBefore := view.Revision

	// The manager holds no lease, and is not required to.
	var result struct {
		LayoutMode      string `json:"layout_mode"`
		FlippedToManual bool   `json:"flipped_to_manual"`
	}
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/diagrams/"+f.did.String()+"/layout", map[string]any{
		"nodes": []map[string]any{{"node_id": view.Nodes[0].ID, "x": 10, "y": 20}},
	}), http.StatusOK, &result)

	if !result.FlippedToManual || result.LayoutMode != "manual" {
		t.Errorf("the first coordinate write did not flip the diagram to manual: %+v", result)
	}

	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/diagrams/"+f.did.String(), nil), http.StatusOK, &view)
	if view.Revision != revisionBefore {
		t.Errorf("a layout write produced a revision: %d -> %d", revisionBefore, view.Revision)
	}

	var revs struct {
		Revisions []struct {
			Seq int `json:"seq"`
		} `json:"revisions"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/diagrams/"+f.did.String()+"/revisions", nil), http.StatusOK, &revs)
	if len(revs.Revisions) != 1 {
		t.Errorf("the revision log has %d entries; a layout write must add none", len(revs.Revisions))
	}
}

// TestDeveloperMayMoveABox: a coordinate is not an owned field (D32).
func TestDeveloperMayMoveABox(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	var view struct {
		Nodes []struct {
			ID uuid.UUID `json:"id"`
		} `json:"nodes"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/diagrams/"+f.did.String(), nil), http.StatusOK, &view)

	f.h.decode(f.dev.do(http.MethodPut, "/api/v1/diagrams/"+f.did.String()+"/layout", map[string]any{
		"nodes": []map[string]any{{"node_id": view.Nodes[0].ID, "x": 5, "y": 5}},
	}), http.StatusOK, nil)
}

// TestAutoLayoutResetNeedsAnExplicitConfirm (§3.2): re-running auto layout on a
// manual diagram is destructive.
func TestAutoLayoutResetNeedsAnExplicitConfirm(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	body := f.h.decodeErr(f.admin.do(http.MethodPost, "/api/v1/diagrams/"+f.did.String()+"/layout/reset",
		map[string]any{"confirm": false}), http.StatusBadRequest)
	if !strings.Contains(body.Error.Message, "cannot be undone") {
		t.Errorf("the refusal does not say the reset is destructive: %q", body.Error.Message)
	}

	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/diagrams/"+f.did.String()+"/layout/reset",
		map[string]any{"confirm": true}), http.StatusNoContent, nil)
}

// TestClosedVocabularyErrorTeaches: the message is read by the vision model on
// ingest and by the planner in Phase 6, so it lists the valid values *with
// their meanings*, not merely the fact of a refusal.
func TestClosedVocabularyErrorTeaches(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)

	body := f.h.decodeErr(f.save(f.admin, map[string]any{
		"base_revision": 0,
		"nodes": []map[string]any{
			{"slug": "a", "kind": "microservice", "name": "A"},
			{"slug": "b", "kind": "service", "name": "B"},
		},
		"edges": []map[string]any{{"from": "a", "to": "b", "relation": "talks_to"}},
	}), http.StatusBadRequest)

	if body.Error.Code != "validation_failed" {
		t.Errorf("code = %q, want validation_failed", body.Error.Code)
	}
	msg := body.Error.Message
	if msg == "validation failed" {
		t.Fatal("the message is huma's default; a model reading it learns nothing")
	}
	// Every valid value, and what each one means.
	for _, want := range []string{"data_flow", "publishes_to", "external_system", "datastore",
		"generic flow, when nothing more specific is true", "a database, cache, object store"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not teach %q:\n%s", want, msg)
		}
	}
}

// TestNoteNodesCarryNoRelations (§3.3), enforced as a graph invariant rather
// than left to the caller.
func TestNoteNodesCarryNoRelations(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)

	body := f.h.decodeErr(f.save(f.admin, map[string]any{
		"base_revision": 0,
		"nodes": []map[string]any{
			{"slug": "a", "kind": "service", "name": "A"},
			{"slug": "sticky", "kind": "note", "name": "Remember the rate limit"},
		},
		"edges": []map[string]any{{"from": "a", "to": "sticky", "relation": "depends_on"}},
	}), http.StatusBadRequest)

	if !strings.Contains(body.Error.Message, "carries no relations") {
		t.Errorf("the refusal does not explain why: %q", body.Error.Message)
	}
}

// TestOnlyBoundariesContain (§3.3).
func TestOnlyBoundariesContain(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)

	body := f.h.decodeErr(f.save(f.admin, map[string]any{
		"base_revision": 0,
		"nodes": []map[string]any{
			{"slug": "a", "kind": "service", "name": "A"},
			{"slug": "b", "kind": "service", "name": "B"},
		},
		"edges": []map[string]any{{"from": "a", "to": "b", "relation": "contains"}},
	}), http.StatusBadRequest)

	if !strings.Contains(body.Error.Message, "only a boundary or a system may contain") {
		t.Errorf("the refusal does not name the rule: %q", body.Error.Message)
	}
}

// TestNameCollisionProducesANoteNotAMerge (§3.4). A merge that is wrong 10% of
// the time is a permanent source of confusion with no permanent fix.
func TestNameCollisionProducesANoteNotAMerge(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	next := initialState(1)
	next["summary"] = "add a session db"
	next["nodes"] = append(next["nodes"].([]map[string]any),
		map[string]any{"slug": "session-db", "kind": "datastore", "name": "token store"})

	var result struct {
		Revision    int `json:"revision"`
		NotesRaised []struct {
			Kind        string `json:"kind"`
			Body        string `json:"body"`
			SubjectSlug string `json:"subject_slug"`
		} `json:"notes_raised"`
	}
	f.h.decode(f.save(f.admin, next), http.StatusCreated, &result)

	if len(result.NotesRaised) != 1 {
		t.Fatalf("expected one overlap note, got %d", len(result.NotesRaised))
	}
	note := result.NotesRaised[0]
	if note.Kind != "overlap" {
		t.Errorf("note kind = %q, want overlap", note.Kind)
	}
	if !strings.Contains(note.Body, "not merged") {
		t.Errorf("the note does not say the two were left alone: %q", note.Body)
	}

	// Both entities survive. Nothing was merged.
	var entities struct {
		Entities []struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"entities"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/entities", nil),
		http.StatusOK, &entities)

	var sawBoth int
	for _, e := range entities.Entities {
		if e.Slug == "token-store" || e.Slug == "session-db" {
			sawBoth++
		}
	}
	if sawBoth != 2 {
		t.Errorf("expected both entities to survive, found %d", sawBoth)
	}

	// Saving the same state again does not restate the same clarification.
	again := initialState(2)
	again["nodes"] = next["nodes"]
	again["edges"] = next["edges"]
	var second struct {
		NotesRaised []any `json:"notes_raised"`
	}
	f.h.decode(f.save(f.admin, again), http.StatusCreated, &second)
	if len(second.NotesRaised) != 0 {
		t.Errorf("the same overlap was raised twice: %v", second.NotesRaised)
	}
}

// TestCrossDiagramDependents is the criterion "what depends on entity X",
// answered across diagrams. This is the part a vector store cannot do.
func TestCrossDiagramDependents(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	// A second diagram, at a different level, placing the same entity.
	var second struct {
		ID uuid.UUID `json:"id"`
	}
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/diagrams",
		map[string]any{"name": "Auth Containers", "level": "container"}), http.StatusCreated, &second)
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/diagrams/"+second.ID.String()+"/lease", nil), http.StatusOK, nil)
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/diagrams/"+second.ID.String()+"/revisions", map[string]any{
		"base_revision": 0, "summary": "containers",
		"nodes": []map[string]any{
			{"slug": "token-store", "kind": "datastore", "name": "Token Store"},
			{"slug": "session-reaper", "kind": "job", "name": "Session Reaper"},
		},
		"edges": []map[string]any{
			{"from": "session-reaper", "to": "token-store", "relation": "reads_from"},
		},
	}), http.StatusCreated, nil)

	var entityID uuid.UUID
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT id FROM catalog_entities WHERE project_id = $1 AND slug = 'token-store'`, f.pid).Scan(&entityID); err != nil {
		t.Fatal(err)
	}

	var detail struct {
		Slug       string `json:"slug"`
		Placements []struct {
			DiagramSlug string `json:"diagram_slug"`
		} `json:"placements"`
		Dependents []struct {
			Slug        string `json:"slug"`
			Depth       int    `json:"depth"`
			Relation    string `json:"relation"`
			DiagramSlug string `json:"diagram_slug"`
		} `json:"dependents"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/entities/"+entityID.String()+"?depth=2", nil),
		http.StatusOK, &detail)

	// One identity, two pictures.
	if len(detail.Placements) != 2 {
		t.Errorf("the entity appears in %d diagrams, want 2", len(detail.Placements))
	}

	found := map[string]int{}
	for _, d := range detail.Dependents {
		found[d.Slug] = d.Depth
	}
	// Direct: auth-service writes to it in one diagram, session-reaper reads
	// from it in the other.
	for _, want := range []string{"auth-service", "session-reaper"} {
		if depth, ok := found[want]; !ok || depth != 1 {
			t.Errorf("%s is not reported as a direct dependent: %v", want, found)
		}
	}
	// Transitive, and only because the traversal is over entities rather than
	// per-diagram nodes.
	if depth, ok := found["browser"]; !ok || depth != 2 {
		t.Errorf("browser is not reported at depth 2: %v", found)
	}
}

// TestStructuralRelationsAreNotDependencies: letting `contains` into the answer
// turns "what depends on the token store" into "everything in the same trust
// boundary" — a traversal, and a useless answer.
func TestStructuralRelationsAreNotDependencies(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, map[string]any{
		"base_revision": 0, "summary": "boundary",
		"nodes": []map[string]any{
			{"slug": "zone", "kind": "boundary", "name": "Trust Zone"},
			{"slug": "store", "kind": "datastore", "name": "Store"},
		},
		"edges": []map[string]any{{"from": "zone", "to": "store", "relation": "contains"}},
	}), http.StatusCreated, nil)

	var entityID uuid.UUID
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT id FROM catalog_entities WHERE project_id = $1 AND slug = 'store'`, f.pid).Scan(&entityID); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Dependents []any `json:"dependents"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/entities/"+entityID.String(), nil), http.StatusOK, &detail)
	if len(detail.Dependents) != 0 {
		t.Errorf("a containment edge was reported as a dependency: %v", detail.Dependents)
	}
}

// TestOneRevisionPerSave, and one audit row with it.
func TestOneRevisionPerSave(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	ctx := context.Background()
	var revisions int
	if err := f.h.db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM diagram_revisions WHERE diagram_id = $1`, f.did).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if revisions != 1 {
		t.Errorf("one save produced %d revisions", revisions)
	}

	var revised int
	if err := f.h.db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'diagram.revised' AND subject_id = $1`, f.did).Scan(&revised); err != nil {
		t.Fatal(err)
	}
	if revised != 1 {
		t.Errorf("one save produced %d diagram.revised audit rows", revised)
	}

	// A save that changes nothing produces neither.
	f.h.decode(f.save(f.admin, initialState(1)), http.StatusCreated, nil)
	if err := f.h.db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM diagram_revisions WHERE diagram_id = $1`, f.did).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if revisions != 1 {
		t.Errorf("a no-op save produced a revision; the log now has %d", revisions)
	}
}

// TestSaveIsAtomic: a save that fails validation leaves nothing behind.
func TestSaveIsAtomic(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	// Valid nodes, then an edge that cannot exist. The nodes must not survive.
	bad := map[string]any{
		"base_revision": 1, "summary": "half a change",
		"nodes": append(initialState(1)["nodes"].([]map[string]any),
			map[string]any{"slug": "cache", "kind": "datastore", "name": "Cache"},
			map[string]any{"slug": "sticky", "kind": "note", "name": "A note"}),
		"edges": append(initialState(1)["edges"].([]map[string]any),
			map[string]any{"from": "cache", "to": "sticky", "relation": "depends_on"}),
	}
	f.h.decodeErr(f.save(f.admin, bad), http.StatusBadRequest)

	body := f.yaml(f.admin)
	if strings.Contains(body, "id: cache") || strings.Contains(body, "id: sticky") {
		t.Errorf("a rejected save left nodes behind:\n%s", body)
	}
	var revisions int
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM diagram_revisions WHERE diagram_id = $1`, f.did).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if revisions != 1 {
		t.Errorf("a rejected save produced a revision; the log has %d", revisions)
	}
}

// TestDiagramSlugSurvivesARename: the git projection derives paths from it, and
// a rename must not look like a delete plus an add (§6.2).
func TestEntitySlugSurvivesARename(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	renamed := initialState(1)
	renamed["summary"] = "rename the auth service"
	nodes := renamed["nodes"].([]map[string]any)
	nodes[0]["name"] = "Authentication Service (v2)"

	f.h.decode(f.save(f.admin, renamed), http.StatusCreated, nil)

	body := f.yaml(f.admin)
	if !strings.Contains(body, "id: auth-service") {
		t.Errorf("the slug changed with the name:\n%s", body)
	}
	if !strings.Contains(body, "name: Authentication Service (v2)") {
		t.Errorf("the rename did not land:\n%s", body)
	}
}

// TestNoteResolutionIsManagerOwned (D31).
func TestNoteResolutionIsManagerOwned(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	next := initialState(1)
	next["nodes"] = append(next["nodes"].([]map[string]any),
		map[string]any{"slug": "session-db", "kind": "datastore", "name": "token store"})
	var result struct {
		NotesRaised []struct {
			ID uuid.UUID `json:"id"`
		} `json:"notes_raised"`
	}
	f.h.decode(f.save(f.admin, next), http.StatusCreated, &result)
	if len(result.NotesRaised) == 0 {
		t.Fatal("no note to resolve")
	}
	noteID := result.NotesRaised[0].ID

	body := f.h.decodeErr(f.dev.do(http.MethodPatch, "/api/v1/notes/"+noteID.String(),
		map[string]any{"status": "resolved"}), http.StatusForbidden)
	if body.Error.Code != "field_not_owned" {
		t.Errorf("code = %q, want field_not_owned", body.Error.Code)
	}

	f.h.decode(f.mgr.do(http.MethodPatch, "/api/v1/notes/"+noteID.String(),
		map[string]any{"status": "resolved"}), http.StatusOK, nil)

	// A resolved note leaves the serialized context: carrying a settled
	// question into every prompt spends tokens re-litigating it.
	if strings.Contains(f.yaml(f.admin), "kind: overlap") {
		t.Error("a resolved note is still serialized")
	}
}

// TestUnplacedNodesAreFlagged (§3.2): a node arriving on a manual diagram is
// placed near its neighbours and marked, so the UI can make it distinct.
func TestUnplacedNodesAreFlagged(t *testing.T) {
	f := newDiagramFixture(t)
	f.h.decode(f.lease(f.admin), http.StatusOK, nil)
	f.h.decode(f.save(f.admin, initialState(0)), http.StatusCreated, nil)

	var view struct {
		Nodes []struct {
			ID   uuid.UUID `json:"id"`
			Slug string    `json:"slug"`
		} `json:"nodes"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/diagrams/"+f.did.String(), nil), http.StatusOK, &view)

	// Make it manual by placing everything.
	placements := []map[string]any{}
	for i, n := range view.Nodes {
		placements = append(placements, map[string]any{"node_id": n.ID, "x": float64(i * 100), "y": 50})
	}
	f.h.decode(f.admin.do(http.MethodPut, "/api/v1/diagrams/"+f.did.String()+"/layout",
		map[string]any{"nodes": placements}), http.StatusOK, nil)

	// Now add a node through a save. It has no coordinates of its own.
	next := initialState(1)
	next["summary"] = "add a cache"
	next["nodes"] = append(next["nodes"].([]map[string]any),
		map[string]any{"slug": "cache", "kind": "datastore", "name": "Cache"})
	next["edges"] = append(next["edges"].([]map[string]any),
		map[string]any{"from": "auth-service", "to": "cache", "relation": "reads_from"})
	f.h.decode(f.save(f.admin, next), http.StatusCreated, nil)

	var after struct {
		Nodes []struct {
			Slug   string `json:"slug"`
			Layout *struct {
				X        *float64 `json:"x"`
				Unplaced bool     `json:"unplaced"`
			} `json:"layout"`
		} `json:"nodes"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/diagrams/"+f.did.String(), nil), http.StatusOK, &after)

	var checked bool
	for _, n := range after.Nodes {
		if n.Slug != "cache" {
			continue
		}
		checked = true
		if n.Layout == nil || !n.Layout.Unplaced {
			t.Errorf("the new node is not flagged unplaced: %+v", n.Layout)
		}
		// Near its neighbour rather than at the origin, which would be a
		// screen away from everything it connects to.
		if n.Layout != nil && n.Layout.X != nil && *n.Layout.X == 0 {
			t.Error("the new node was placed at the origin rather than near its neighbours")
		}
	}
	if !checked {
		t.Fatal("the new node is not in the view")
	}
}
