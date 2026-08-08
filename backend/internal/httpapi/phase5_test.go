package httpapi_test

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/contextbuild"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/contextsvc"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
	"github.com/Felodynn/ZettaPlanner/backend/internal/vector"
)

// Phase 5's exit criteria, over a real Postgres.
//
// These go through the context service directly rather than over HTTP: context
// assembly has no endpoint of its own — it is what a turn receives — and the
// thing worth testing is the assembled bytes, not a JSON envelope around them.

type contextFixture struct {
	*planFixture
	svc *contextsvc.Service
	ctx context.Context
	who policy.Principal
}

func newContextFixture(t *testing.T) *contextFixture {
	t.Helper()
	f := newPlanFixture(t)

	// No registry: these tests run without an Ollama, so retrieval reports
	// itself unavailable. That is deliberate — it means every assertion below
	// about expansion is an assertion that traversal alone produced it.
	svc := contextsvc.New(f.h.db, slog.New(slog.DiscardHandler), nil)
	ctx := telemetry.WithRequestID(context.Background(), telemetry.NewRequestID())

	actor, err := repo.Actors{}.ForUser(ctx, f.h.db.Pool, f.admin.userID)
	if err != nil {
		t.Fatal(err)
	}
	return &contextFixture{
		planFixture: f,
		svc:         svc,
		ctx:         ctx,
		who: policy.Principal{
			UserID: f.admin.userID, ActorID: actor.ID,
			GlobalRole: policy.RoleAdmin, IsActive: true,
		},
	}
}

func (f *contextFixture) assemble(t *testing.T, req contextsvc.Request) *contextsvc.Result {
	t.Helper()
	res, err := f.svc.Assemble(f.ctx, f.pid, req, f.who)
	if err != nil {
		t.Fatalf("assembling: %v", err)
	}
	return res
}

// decide writes a decided proposal directly.
//
// Phase 7 builds the proposal service; the skeleton's slot for rejections
// exists now because CONTRACT §5.2 puts it there, and a slot wired up later is
// a slot somebody forgets to wire up.
func (f *contextFixture) decide(t *testing.T, status, title, rationale, decision string) {
	t.Helper()
	_, err := f.h.db.Pool.Exec(f.ctx, `
		INSERT INTO proposals (id, project_id, actor_id, title, rationale, target_kind,
		                       status, decided_by, decided_at, decision_rationale, created_at)
		VALUES ($1, $2, $3, $4, $5, 'plan', $6, $7, now(), $8, now())`,
		uuid.Must(uuid.NewV7()), f.pid, f.who.ActorID, title, rationale, status, f.admin.userID, decision)
	if err != nil {
		t.Fatal(err)
	}
}

// ── the exit criteria ────────────────────────────────────────────────────────

// TestSkeletonCarriesEverythingItMust is the first exit criterion: open items,
// deviations, notes, recent decisions, and rejected proposals with rationales.
func TestSkeletonCarriesEverythingItMust(t *testing.T) {
	f := newContextFixture(t)

	blocked := f.createItem(f.mgr, map[string]any{
		"title": "Wire the planner agent", "assignee_id": f.dev.userID.String(),
		"milestone_id": f.msID.String(), "priority": "high", "due_date": "2026-09-01",
	})
	f.h.decode(f.patchItem(f.dev, blocked.ID, map[string]any{"status": "blocked"}), http.StatusOK, nil)
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/deviations", map[string]any{
		"work_item_id": blocked.ID.String(),
		"summary":      "the token store cannot do the range query we planned around",
		"severity":     "high",
	}), http.StatusCreated, nil)

	f.decide(t, "approved", "Split auth into two services",
		"one process was doing two jobs", "agreed, and the seam is where the token store sits")
	f.decide(t, "rejected", "Merge token-store into session-db",
		"they look like the same thing", "we tried exactly this in April; the retention rules differ")

	res := f.assemble(t, contextsvc.Request{Message: "what should I do next?"})
	text := res.Text

	for _, want := range []string{
		"## PROJECT", "ZettaPlanner", "v0 backend",
		"## OPEN WORK", blocked.Ref, "Wire the planner agent", "blocked",
		"## OPEN DEVIATIONS", "range query we planned around",
		"## RECENT DECISIONS", "Split auth into two services",
		"## PREVIOUSLY REJECTED", "Merge token-store into session-db",
		"we tried exactly this in April",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the skeleton is missing %q\n\n%s", want, text)
		}
	}

	// The rationale is the point. A rejection without one is a rejection the
	// agent learns nothing from, and it re-proposes the same thing next week.
	rejected := text[strings.Index(text, "## PREVIOUSLY REJECTED"):]
	if !strings.Contains(rejected, "REJECTED because:") {
		t.Error("a rejection was listed without the reason it was refused")
	}
}

// TestExpansionAnswersByTraversal is the second exit criterion — and the
// assertion that no vector search was involved is structural: this fixture has
// no embedding model at all, so everything below arrived by query.
func TestExpansionAnswersByTraversal(t *testing.T) {
	f := newContextFixture(t)

	// A small architecture, through the Phase 2 path.
	var diagram struct {
		ID uuid.UUID `json:"id"`
	}
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/diagrams",
		map[string]any{"name": "Auth Context", "level": "context"}), http.StatusCreated, &diagram)
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+diagram.ID.String()+"/lease", nil), http.StatusOK, nil)
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+diagram.ID.String()+"/revisions", map[string]any{
		"base_revision": 0, "summary": "initial",
		"nodes": []map[string]any{
			{"slug": "auth-service", "kind": "service", "name": "Auth Service", "summary": "Issues session tokens"},
			{"slug": "token-store", "kind": "datastore", "name": "Token Store"},
			{"slug": "browser", "kind": "actor", "name": "Browser"},
		},
		"edges": []map[string]any{
			{"from": "auth-service", "to": "token-store", "relation": "writes_to", "label": "session rows"},
			{"from": "browser", "to": "auth-service", "relation": "calls", "label": "POST /auth/token"},
		},
	}), http.StatusCreated, nil)

	var entities struct {
		Entities []struct {
			ID   uuid.UUID `json:"id"`
			Slug string    `json:"slug"`
		} `json:"entities"`
	}
	f.h.decode(f.mgr.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/entities", nil), http.StatusOK, &entities)
	bySlug := map[string]uuid.UUID{}
	for _, e := range entities.Entities {
		bySlug[e.Slug] = e.ID
	}

	blocker := f.createItem(f.mgr, map[string]any{"title": "Rotate the signing key"})
	item := f.createItem(f.mgr, map[string]any{
		"title": "Session expiry", "assignee_id": f.dev.userID.String(), "milestone_id": f.msID.String(),
	})
	downstream := f.createItem(f.mgr, map[string]any{"title": "Logout everywhere"})

	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+blocker.ID.String()+"/dependencies",
		map[string]any{"blocks": []string{item.ID.String()}}), http.StatusOK, nil)
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+item.ID.String()+"/dependencies",
		map[string]any{"blocks": []string{downstream.ID.String()}}), http.StatusOK, nil)
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+item.ID.String()+"/entity-refs",
		map[string]any{"implements": []string{bySlug["auth-service"].String()}}), http.StatusOK, nil)
	f.h.decode(f.dev.do(http.MethodPost, "/api/v1/work-items/"+item.ID.String()+"/sub-tasks",
		map[string]any{"title": "decide the TTL"}), http.StatusCreated, nil)

	res := f.assemble(t, contextsvc.Request{Message: "why is " + item.Ref + " not done?"})
	text := res.Text

	if len(res.References.ItemRefs) != 1 || res.References.ItemRefs[0] != item.Ref {
		t.Fatalf("expansion started from %v, want just %s", res.References.ItemRefs, item.Ref)
	}
	for _, want := range []string{
		"## REFERENCED WORK", item.Ref, "Session expiry",
		"blocked by: " + blocker.Ref, "blocks: " + downstream.Ref,
		"implements: auth-service",
		"decide the TTL",
		// The architecture slice: the entity the item implements, plus one hop
		// in each direction, each hop naming the diagram it was drawn in.
		"## ARCHITECTURE", "auth-service (service)",
		"writes_to token-store", "calls browser", "drawn in auth-context",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expansion is missing %q\n\n%s", want, text)
		}
	}

	// One hop, not two. `browser` is reached from auth-service; what browser
	// touches beyond that is most of a project's architecture arriving as
	// though it were all relevant.
	if strings.Count(text, "\nbrowser (actor)") > 0 {
		t.Error("the slice expanded a second hop into its own entity block")
	}

	// And none of it came from retrieval.
	if res.Input.Spill.Unavailable == "" {
		t.Error("this fixture has no embedding model; spill should have said so")
	}
	if len(res.Input.Spill.Hits) != 0 {
		t.Error("expansion must not be answered by similarity search")
	}
}

// TestSpillRefusesAMixedEmbeddingSpace is the third exit criterion's sharp end.
//
// A corpus half in one embedding space and half in another returns plausible
// neighbours that are simply wrong — the failure mode with no error message.
func TestSpillRefusesAMixedEmbeddingSpace(t *testing.T) {
	f := newContextFixture(t)
	chunks := repo.NewChunks(f.h.db.Pool)

	write := func(model, body string) {
		t.Helper()
		if err := chunks.Upsert(f.ctx, []vector.Chunk{{
			ID: uuid.Must(uuid.NewV7()), ProjectID: f.pid,
			SourceKind: vector.SourceWorklog, SourceID: uuid.Must(uuid.NewV7()),
			Index: 0, Content: body, Embedding: unitVector(0.1), Model: model, CreatedAt: time.Now(),
		}}); err != nil {
			t.Fatal(err)
		}
	}

	write("bge-m3", "the first thing anybody wrote down")
	hits, err := chunks.Search(f.ctx, vector.Query{
		ProjectID: f.pid, Embedding: unitVector(0.1), Model: "bge-m3", Limit: 5,
	})
	if err != nil {
		t.Fatalf("a single-model corpus should search cleanly: %v", err)
	}
	if len(hits) != 1 || hits[0].Model != "bge-m3" {
		t.Fatalf("expected one hit recorded against bge-m3, got %+v", hits)
	}
	if hits[0].Score < 0.99 {
		t.Errorf("an identical vector should score ~1, got %v", hits[0].Score)
	}

	// Now half-migrate the corpus, which is exactly what changing the embedding
	// model without re-embedding looks like.
	write("nomic-embed-text", "the second thing, embedded by a different model")
	_, err = chunks.Search(f.ctx, vector.Query{
		ProjectID: f.pid, Embedding: unitVector(0.1), Model: "bge-m3", Limit: 5,
	})
	var mismatch *vector.ErrModelMismatch
	if !asErr(err, &mismatch) {
		t.Fatalf("a mixed corpus must be refused, got %v", err)
	}
	for _, want := range []string{"bge-m3", "nomic-embed-text", "re-embed"} {
		if !strings.Contains(mismatch.Error(), want) {
			t.Errorf("the refusal does not mention %q: %s", want, mismatch.Error())
		}
	}

	// A vector of the wrong length is caught before it reaches the column, so
	// the message names the cause rather than a type and a number.
	err = chunks.Upsert(f.ctx, []vector.Chunk{{
		ProjectID: f.pid, SourceKind: vector.SourceWorklog, SourceID: uuid.Must(uuid.NewV7()),
		Content: "x", Embedding: make([]float32, 768), Model: "some-other-model", CreatedAt: time.Now(),
	}})
	if err == nil || !strings.Contains(err.Error(), "re-embedding the corpus") {
		t.Errorf("a wrong-dimension vector should be refused with the reason: %v", err)
	}
}

// TestSpillSaysWhyItIsEmpty: three of the four ways retrieval returns nothing
// mean "nothing was looked at", and an empty section cannot say which.
func TestSpillSaysWhyItIsEmpty(t *testing.T) {
	f := newContextFixture(t)
	res := f.assemble(t, contextsvc.Request{Message: "anything at all"})

	if !strings.Contains(res.Text, "## FROM THE LOG") {
		t.Fatal("the retrieval section should be present and say why it is empty")
	}
	if !strings.Contains(res.Text, "no retrieval was performed:") {
		t.Errorf("the section does not distinguish 'nothing was found' from 'nothing was looked at':\n%s", res.Text)
	}
	if res.Input.Spill.Unavailable == "" {
		t.Error("the reason is not carried in the input either, so nothing can report it")
	}
	// And it is a drop, so it appears in the OMITTED list a reader scans first.
	var recorded bool
	for _, d := range res.Context.Drops {
		if d.Layer == contextbuild.LayerSpill && d.Why == res.Input.Spill.Unavailable {
			recorded = true
		}
	}
	if !recorded {
		t.Error("an unavailable retrieval layer should be recorded as a drop with its reason")
	}
}

// TestDigestHarnessCannotDrift is the fourth exit criterion.
//
// The stored row is a cache that nothing depends on. Corrupting it and reading
// again must produce the right numbers, because every read recomputes.
func TestDigestHarnessCannotDrift(t *testing.T) {
	f := newContextFixture(t)
	a := f.createItem(f.mgr, map[string]any{"title": "One", "assignee_id": f.dev.userID.String()})
	f.createItem(f.mgr, map[string]any{"title": "Two"})
	f.h.decode(f.patchItem(f.dev, a.ID, map[string]any{"status": "blocked"}), http.StatusOK, nil)

	first, err := f.svc.Digest(f.ctx, f.pid, f.who)
	if err != nil {
		t.Fatal(err)
	}
	if first.Harness.OpenItems != 2 || len(first.Harness.BlockedRefs) != 1 {
		t.Fatalf("harness = %+v, want 2 open and 1 blocked", first.Harness)
	}
	if first.Harness.BlockedRefs[0] != a.Ref {
		t.Errorf("blocked ref = %v, want %s", first.Harness.BlockedRefs, a.Ref)
	}
	if first.Harness.UnassignedOpen != 1 {
		t.Errorf("unassigned open = %d, want 1", first.Harness.UnassignedOpen)
	}

	// Vandalise the cache the way a stale incremental update would.
	if _, err := f.h.db.Pool.Exec(f.ctx,
		`UPDATE project_digest SET harness_fields = '{"open_items": 999, "blocked_refs": ["ZP-404"]}'::jsonb
		  WHERE project_id = $1`, f.pid); err != nil {
		t.Fatal(err)
	}

	again, err := f.svc.Digest(f.ctx, f.pid, f.who)
	if err != nil {
		t.Fatal(err)
	}
	if again.Harness.OpenItems != 2 {
		t.Errorf("open items = %d after the stored copy was corrupted; the harness half must be recomputed, "+
			"not read", again.Harness.OpenItems)
	}
	if len(again.Harness.BlockedRefs) != 1 || again.Harness.BlockedRefs[0] != a.Ref {
		t.Errorf("blocked refs = %v; the stored copy won, which means it can drift", again.Harness.BlockedRefs)
	}
}

// TestModelFieldsGoStaleWhenTheGraphMoves is the other half of the fourth
// criterion.
//
// Staleness is derived from the audit log's high-water mark rather than set by
// a flag, so it is correct without every write path remembering to set one.
func TestModelFieldsGoStaleWhenTheGraphMoves(t *testing.T) {
	f := newContextFixture(t)
	f.createItem(f.mgr, map[string]any{"title": "Something"})

	hypothesis := contextbuild.ModelFields{
		CurrentHypothesis: "the token store is the bottleneck",
		DeadEnds:          []string{"sharding by tenant — the query pattern does not partition"},
		OpenQuestions:     []string{"does the range query actually matter?"},
	}
	if err := f.svc.SetModelFields(f.ctx, f.pid, hypothesis, f.who); err != nil {
		t.Fatal(err)
	}

	fresh, err := f.svc.Digest(f.ctx, f.pid, f.who)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ModelStale {
		t.Error("model fields written against the current state should not be stale")
	}
	if fresh.Model.CurrentHypothesis != hypothesis.CurrentHypothesis {
		t.Errorf("the hypothesis did not survive: %+v", fresh.Model)
	}

	// Anything recorded in the project moves the mark.
	f.createItem(f.mgr, map[string]any{"title": "Something else"})

	moved, err := f.svc.Digest(f.ctx, f.pid, f.who)
	if err != nil {
		t.Fatal(err)
	}
	if !moved.ModelStale {
		t.Error("the graph moved and the model fields were not marked stale")
	}
	// Stale, not gone. It is a hypothesis with a date on it, and deleting it
	// would lose the dead ends — which are the most valuable thing in it.
	if moved.Model.CurrentHypothesis != hypothesis.CurrentHypothesis {
		t.Error("going stale destroyed the model fields; stale means dated, not wrong")
	}
	if len(moved.Model.DeadEnds) != 1 {
		t.Errorf("the dead ends were lost: %+v", moved.Model)
	}

	// And recomputing the harness half must not clear the model half.
	if moved.Harness.WorkItems != 2 {
		t.Errorf("work items = %d, want 2", moved.Harness.WorkItems)
	}
}

// TestNoLayoutDataReachesTheAssembledContext is the sixth exit criterion,
// checked at runtime as well as by the import test.
//
// The import test proves contextbuild cannot reach the layout store. This
// proves the bytes a model actually receives carry no geometry even when
// coordinates exist — belt and braces, because the two would fail for
// different reasons.
func TestNoLayoutDataReachesTheAssembledContext(t *testing.T) {
	f := newContextFixture(t)

	var diagram struct {
		ID uuid.UUID `json:"id"`
	}
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/diagrams",
		map[string]any{"name": "Auth Context", "level": "context"}), http.StatusCreated, &diagram)
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+diagram.ID.String()+"/lease", nil), http.StatusOK, nil)
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+diagram.ID.String()+"/revisions", map[string]any{
		"base_revision": 0, "summary": "initial",
		"nodes": []map[string]any{{"slug": "auth-service", "kind": "service", "name": "Auth Service"}},
		"edges": []map[string]any{},
	}), http.StatusCreated, nil)

	var view struct {
		Nodes []struct {
			ID uuid.UUID `json:"id"`
		} `json:"nodes"`
	}
	f.h.decode(f.mgr.do(http.MethodGet, "/api/v1/diagrams/"+diagram.ID.String(), nil), http.StatusOK, &view)
	// Store real coordinates, so this is not passing merely because none exist.
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/diagrams/"+diagram.ID.String()+"/layout", map[string]any{
		"nodes": []map[string]any{{"node_id": view.Nodes[0].ID, "x": 1234.5, "y": 987.5, "w": 180, "h": 80}},
	}), http.StatusOK, nil)

	res := f.assemble(t, contextsvc.Request{Message: "tell me about auth-service"})
	if !strings.Contains(res.Text, "auth-service") {
		t.Fatal("the entity is missing, so this test is not checking anything")
	}

	forbidden := regexp.MustCompile(`(?i)\b(x|y|w|h|width|height|waypoints?|collapsed|unplaced|position|layout)\s*[:=]`)
	if m := forbidden.FindString(res.Text); m != "" {
		t.Errorf("the assembled context carries presentation data (%q):\n%s", m, res.Text)
	}
	for _, coord := range []string{"1234.5", "987.5"} {
		if strings.Contains(res.Text, coord) {
			t.Errorf("a stored coordinate (%s) reached the assembled context", coord)
		}
	}
}

// TestAssembledContextIsByteIdentical: two assemblies of an unchanged project
// produce the same bytes apart from the timestamp in the header.
func TestAssembledContextIsByteIdentical(t *testing.T) {
	f := newContextFixture(t)
	for i := 0; i < 5; i++ {
		f.createItem(f.mgr, map[string]any{"title": "Item " + string(rune('A'+i))})
	}

	strip := func(s string) string {
		var out []string
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(line, "# assembled:") {
				continue
			}
			out = append(out, line)
		}
		return strings.Join(out, "\n")
	}

	first := strip(f.assemble(t, contextsvc.Request{Message: "what next?"}).Text)
	for i := 0; i < 3; i++ {
		if got := strip(f.assemble(t, contextsvc.Request{Message: "what next?"}).Text); got != first {
			t.Fatalf("assembly %d differs; a context that varies for no reason is an answer that varies for no reason", i)
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// unitVector builds a vector of the configured dimension with one non-zero
// component, so two calls with the same seed are identical and cosine
// similarity is 1.
func unitVector(seed float32) []float32 {
	v := make([]float32, vector.Dimensions)
	v[0] = seed
	v[1] = seed / 2
	return v
}

func asErr[T error](err error, target *T) bool {
	for err != nil {
		if t, ok := err.(T); ok {
			*target = t
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
