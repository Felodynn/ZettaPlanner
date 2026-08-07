package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/config"
	"github.com/Felodynn/ZettaPlanner/backend/internal/httpapi"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/auth"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	ingestsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/ingest"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/project"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/testdb"
)

// Phase 3's exit criteria, over the real HTTP surface, a real Postgres, and a
// fake model server.
//
// The model is faked and nothing else is. What is being tested here is the
// harness around the model — per-page isolation, the merge, the refusal to
// invent a name — and those rules must hold for *any* model output, including
// output no real model would produce. The real model is exercised by the manual
// test with an actual photograph, which is the only thing that can answer
// whether the reading is any good.

// ── the fake model server ────────────────────────────────────────────────────

// fakeOllama answers /api/chat with whatever the test scripted for the page it
// was sent.
//
// The page is identified by the red channel of the image's first pixel, so a
// scripted answer follows its page even though extraction is concurrent and
// finishes in whatever order it finishes in.
type fakeOllama struct {
	srv *httptest.Server

	mu       sync.Mutex
	byPage   map[int][]string // queued raw response bodies, consumed in order
	requests []fakeRequest
}

type fakeRequest struct {
	page     int
	messages []map[string]any
	format   json.RawMessage
	think    *bool
}

func newFakeOllama(t *testing.T) *fakeOllama {
	t.Helper()
	f := &fakeOllama{byPage: map[int][]string{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
			Format   json.RawMessage  `json:"format"`
			Think    *bool            `json:"think"`
			Options  struct {
				NumCtx int `json:"num_ctx"`
			} `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("fake ollama: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// D4: num_ctx is on every request or the window is whatever the server
		// felt like. Asserted here so a regression shows up as a test failure
		// rather than as an agent with amnesia six months later.
		if body.Options.NumCtx == 0 {
			t.Error("fake ollama: request carried no num_ctx")
		}

		page := pageOfRequest(t, body.Messages)

		f.mu.Lock()
		f.requests = append(f.requests, fakeRequest{
			page: page, messages: body.Messages, format: body.Format, think: body.Think,
		})
		queued := f.byPage[page]
		var content string
		switch {
		case len(queued) == 0:
			content = `{"nodes":[],"edges":[]}`
		case len(queued) == 1:
			content = queued[0] // the last scripted answer repeats
		default:
			content = queued[0]
			f.byPage[page] = queued[1:]
		}
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":             "fake-vision",
			"message":           map[string]any{"role": "assistant", "content": content},
			"done":              true,
			"done_reason":       "stop",
			"prompt_eval_count": 1234,
			"eval_count":        56,
		})
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// script queues the raw content the model returns for one page. The last entry
// repeats for any further calls.
func (f *fakeOllama) script(page int, contents ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byPage[page] = contents
}

func (f *fakeOllama) callsFor(page int) []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeRequest
	for _, r := range f.requests {
		if r.page == page {
			out = append(out, r)
		}
	}
	return out
}

// pageOfRequest reads the page index back out of the image the model was sent.
func pageOfRequest(t *testing.T, messages []map[string]any) int {
	t.Helper()
	for _, m := range messages {
		raw, ok := m["images"].([]any)
		if !ok || len(raw) == 0 {
			continue
		}
		encoded, _ := raw[0].(string)
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Errorf("fake ollama: image is not base64: %v", err)
			return -1
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Errorf("fake ollama: image is not a PNG: %v", err)
			return -1
		}
		r, _, _, _ := img.At(0, 0).RGBA()
		return int(r >> 8)
	}
	t.Error("fake ollama: a vision request arrived with no image")
	return -1
}

// pageImage builds a PNG whose first pixel encodes its index.
func pageImage(t *testing.T, idx int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	for y := 0; y < 48; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.White)
		}
	}
	img.Set(0, 0, color.RGBA{R: uint8(idx), A: 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ── harness ──────────────────────────────────────────────────────────────────

type ingestFixture struct {
	h     *harness
	model *fakeOllama
	admin *client
	dev   *client
	pid   uuid.UUID
}

func newIngestFixture(t *testing.T) *ingestFixture {
	t.Helper()
	db, ctx := testdb.Fresh(t, "httpapi_ingest")
	quiet := slog.New(slog.DiscardHandler)
	if _, err := store.MigrateUp(ctx, db, quiet); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	model := newFakeOllama(t)
	registry := llm.NewRegistry(&config.Models{
		Path: "test",
		Models: map[string]*config.Model{
			"fake-vision": {
				Name: "fake-vision", Endpoint: model.srv.URL, Model: "fake-vision",
				Tier: config.TierSelfHosted, Caps: []config.Cap{config.CapVision},
				NumCtx: 32768, Concurrency: 1,
				KeepAlive: config.Duration(5 * time.Minute),
				Timeout:   config.Duration(30 * time.Second),
			},
		},
		Roles: map[string]*config.RoleSpec{
			"vision": {Requires: []config.Cap{config.CapVision}, Prefer: []string{"fake-vision"}},
		},
	})

	diagrams := diagramsvc.New(db, quiet, 0)
	ingests := ingestsvc.New(db, quiet, registry, diagrams, t.TempDir())
	t.Cleanup(ingests.Wait)

	api := httpapi.New(httpapi.Deps{
		DB:       db,
		Auth:     auth.New(db, quiet, 0),
		Projects: project.New(db, quiet),
		Diagrams: diagrams,
		Ingests:  ingests,
		Registry: registry,
		Log:      quiet,
	})
	srv := httptest.NewServer(api.Handler)
	t.Cleanup(srv.Close)

	h := &harness{t: t, srv: srv, db: db}
	admin := h.bootstrapAdmin()
	dev := admin.createUser("dev@test.local", "Dev", policy.RoleDeveloper)
	pid := h.createProject(admin, "ZP", "ZettaPlanner")
	h.decode(admin.do(http.MethodPut,
		"/api/v1/projects/"+pid.String()+"/members/"+dev.userID.String(),
		map[string]any{"role": "developer"}), http.StatusNoContent, nil)

	return &ingestFixture{h: h, model: model, admin: admin, dev: dev, pid: pid}
}

// upload posts n page images as one multipart ingest.
func (f *ingestFixture) upload(c *client, pages int) ingestBody {
	f.h.t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for i := 0; i < pages; i++ {
		part, err := w.CreateFormFile("files", fmt.Sprintf("page%d.png", i+1))
		if err != nil {
			f.h.t.Fatal(err)
		}
		if _, err := part.Write(pageImage(f.h.t, i)); err != nil {
			f.h.t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		f.h.t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost,
		f.h.srv.URL+"/api/v1/projects/"+f.pid.String()+"/ingests", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.h.t.Fatal(err)
	}

	var out ingestBody
	f.h.decode(resp, http.StatusCreated, &out)
	return out
}

// settle polls until the ingest reaches a state a human can act on.
func (f *ingestFixture) settle(c *client, id uuid.UUID) ingestBody {
	f.h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var out ingestBody
		f.h.decode(c.do(http.MethodGet, "/api/v1/ingests/"+id.String(), nil), http.StatusOK, &out)
		if out.Status == "review" || out.Status == "failed" || out.Status == "accepted" {
			return out
		}
		if time.Now().After(deadline) {
			f.h.t.Fatalf("ingest %s never settled; stuck in %q", id, out.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type ingestBody struct {
	ID          uuid.UUID `json:"id"`
	Status      string    `json:"status"`
	SourceKind  string    `json:"source_kind"`
	PageCount   int       `json:"page_count"`
	ErrorClass  string    `json:"error_class"`
	ErrorDetail string    `json:"error_detail"`
	Pages       []struct {
		Idx         int             `json:"idx"`
		Status      string          `json:"status"`
		Extracted   json.RawMessage `json:"extracted"`
		Raw         string          `json:"raw"`
		Model       string          `json:"model"`
		TokensIn    int             `json:"tokens_in"`
		Attempts    int             `json:"attempts"`
		ErrorClass  string          `json:"error_class"`
		ErrorDetail string          `json:"error_detail"`
	} `json:"pages"`
	Merged *struct {
		Nodes []struct {
			Slug         string   `json:"slug"`
			Name         string   `json:"name"`
			Kind         string   `json:"kind"`
			Parent       string   `json:"parent"`
			Confidence   float64  `json:"confidence"`
			Legible      bool     `json:"legible"`
			Verbatim     string   `json:"verbatim"`
			Pages        []int    `json:"pages"`
			NeedsReview  bool     `json:"needs_review"`
			ReviewReason string   `json:"review_reason"`
			_            struct{} `json:"-"`
		} `json:"nodes"`
		Edges []struct {
			From         string  `json:"from"`
			To           string  `json:"to"`
			Relation     string  `json:"relation"`
			Confidence   float64 `json:"confidence"`
			CrossPage    bool    `json:"cross_page"`
			Unresolved   bool    `json:"unresolved"`
			NeedsReview  bool    `json:"needs_review"`
			ReviewReason string  `json:"review_reason"`
		} `json:"edges"`
		Conflicts []struct {
			Kind   string `json:"kind"`
			Detail string `json:"detail"`
		} `json:"conflicts"`
		PagesRead   []int `json:"pages_read"`
		PagesFailed []int `json:"pages_failed"`
	} `json:"merged"`
	Counts *struct {
		Nodes         int `json:"nodes"`
		Edges         int `json:"edges"`
		NeedsReview   int `json:"needs_review"`
		Illegible     int `json:"illegible"`
		Unresolved    int `json:"unresolved"`
		LowConfidence int `json:"low_confidence"`
	} `json:"counts"`
}

func (b ingestBody) page(t *testing.T, idx int) struct {
	Idx         int             `json:"idx"`
	Status      string          `json:"status"`
	Extracted   json.RawMessage `json:"extracted"`
	Raw         string          `json:"raw"`
	Model       string          `json:"model"`
	TokensIn    int             `json:"tokens_in"`
	Attempts    int             `json:"attempts"`
	ErrorClass  string          `json:"error_class"`
	ErrorDetail string          `json:"error_detail"`
} {
	t.Helper()
	for _, p := range b.Pages {
		if p.Idx == idx {
			return p
		}
	}
	t.Fatalf("no page %d in the ingest", idx)
	panic("unreachable")
}

// ── the exit criteria ────────────────────────────────────────────────────────

func TestExtractionIsPerPageAndMergeIsASeparateStep(t *testing.T) {
	f := newIngestFixture(t)

	// Page 1 draws the auth service with an arrow running off the sheet.
	f.model.script(0, `{
	  "title": "Auth", "level": "context",
	  "nodes": [{"name":"Auth Service","kind":"service","confidence":0.9,"legible":true}],
	  "edges": [{"from":"Auth Service","to":"Token Store","relation":"writes_to","confidence":0.8,"continues_off_page":true}]
	}`)
	// Page 2 has the other end of that arrow.
	f.model.script(1, `{
	  "nodes": [{"name":"Token Store","kind":"datastore","confidence":0.9,"legible":true}],
	  "edges": []
	}`)

	created := f.upload(f.admin, 2)
	if created.Status != "uploaded" || created.PageCount != 2 {
		t.Fatalf("upload returned %+v", created)
	}
	got := f.settle(f.admin, created.ID)
	if got.Status != "review" {
		t.Fatalf("status = %q, want review: %s", got.Status, got.ErrorDetail)
	}

	// One model call per page, never one for the document.
	for _, page := range []int{0, 1} {
		if n := len(f.model.callsFor(page)); n != 1 {
			t.Errorf("page %d got %d model calls, want exactly 1", page, n)
		}
	}

	// The raw per-page output is kept, forever. When extraction is wrong this
	// is the evidence, and re-running costs tokens and does not reproduce.
	for _, page := range []int{0, 1} {
		p := got.page(t, page)
		if p.Status != "extracted" {
			t.Errorf("page %d status = %q", page, p.Status)
		}
		if len(p.Extracted) == 0 || string(p.Extracted) == "null" {
			t.Errorf("page %d kept no raw extraction", page)
		}
		if p.Model == "" || p.TokensIn == 0 {
			t.Errorf("page %d recorded no model or token count: %+v", page, p)
		}
	}
	if !strings.Contains(string(got.page(t, 0).Extracted), "Token Store") {
		t.Error("page 1's raw output should still mention the box it could not place")
	}

	// The merge is a separate, reviewable thing: it joined the two pages and
	// said so.
	if got.Merged == nil || len(got.Merged.Nodes) != 2 {
		t.Fatalf("merge produced %+v", got.Merged)
	}
	if len(got.Merged.Edges) != 1 {
		t.Fatalf("want the cross-page edge, got %+v", got.Merged.Edges)
	}
	e := got.Merged.Edges[0]
	if e.From != "auth-service" || e.To != "token-store" {
		t.Errorf("edge = %s -> %s, want both ends resolved across pages", e.From, e.To)
	}
	if !e.CrossPage || !e.NeedsReview || e.Unresolved {
		t.Errorf("a cross-page join must be flagged for review, not smoothed over: %+v", e)
	}
}

func TestEveryExtractedElementCarriesProvenanceAndConfidence(t *testing.T) {
	f := newIngestFixture(t)
	f.model.script(0, `{
	  "title": "Auth", "level": "context",
	  "nodes": [
	    {"name":"Auth Service","kind":"service","confidence":0.92,"legible":true},
	    {"name":"Token Store","kind":"datastore","confidence":0.45,"legible":true}
	  ],
	  "edges": [{"from":"Auth Service","to":"Token Store","relation":"writes_to","confidence":0.88}]
	}`)

	created := f.upload(f.admin, 1)
	f.settle(f.admin, created.ID)

	var accepted struct {
		DiagramID  uuid.UUID `json:"diagram_id"`
		Revision   int       `json:"revision"`
		NodesAdded int       `json:"nodes_added"`
		EdgesAdded int       `json:"edges_added"`
		Notes      []struct {
			Kind string `json:"kind"`
			Body string `json:"body"`
		} `json:"notes"`
	}
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/ingests/"+created.ID.String()+"/accept",
		map[string]any{"name": "Auth Context", "level": "context"}), http.StatusCreated, &accepted)

	if accepted.NodesAdded != 2 || accepted.EdgesAdded != 1 {
		t.Fatalf("accept applied %d nodes and %d edges", accepted.NodesAdded, accepted.EdgesAdded)
	}

	var view struct {
		Nodes []struct {
			Slug       string   `json:"slug"`
			Provenance string   `json:"provenance"`
			Confidence *float64 `json:"confidence"`
		} `json:"nodes"`
		Edges []struct {
			Provenance string   `json:"provenance"`
			Confidence *float64 `json:"confidence"`
		} `json:"edges"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/diagrams/"+accepted.DiagramID.String(), nil),
		http.StatusOK, &view)

	for _, n := range view.Nodes {
		if n.Provenance != "vlm_extracted" {
			t.Errorf("node %s provenance = %q, want vlm_extracted", n.Slug, n.Provenance)
		}
		if n.Confidence == nil {
			t.Errorf("node %s carries no confidence", n.Slug)
		}
	}
	for _, e := range view.Edges {
		if e.Provenance != "vlm_extracted" || e.Confidence == nil {
			t.Errorf("edge lost its provenance or confidence: %+v", e)
		}
	}

	// The 0.45 reading was accepted as it stands — with a Note saying so, in one
	// place rather than as a badge on every element nobody reads.
	var found bool
	for _, n := range accepted.Notes {
		if n.Kind == "clarification" && strings.Contains(n.Body, "token-store") {
			found = true
		}
	}
	if !found {
		t.Errorf("a low-confidence element was accepted with no clarification note: %+v", accepted.Notes)
	}
}

// TestAcceptWillNotInventANameForAnUnreadableBox is the rule with teeth. A
// verifier with only pass/fail picks confidently and wrongly here, so there is
// no threshold: either a human supplies the name or the element is dropped.
func TestAcceptWillNotInventANameForAnUnreadableBox(t *testing.T) {
	f := newIngestFixture(t)
	f.model.script(0, `{
	  "nodes": [
	    {"name":"Auth Service","kind":"service","confidence":0.9,"legible":true},
	    {"name":"box below the auth service","kind":"datastore","confidence":0.2,"legible":false,"verbatim":"T?k?n St—"}
	  ],
	  "edges": []
	}`)

	created := f.upload(f.admin, 1)
	got := f.settle(f.admin, created.ID)
	if got.Counts == nil || got.Counts.Illegible != 1 {
		t.Fatalf("the unreadable box was not surfaced: %+v", got.Counts)
	}

	var unreadableSlug string
	for _, n := range got.Merged.Nodes {
		if !n.Legible {
			unreadableSlug = n.Slug
			if n.Verbatim != "T?k?n St—" {
				t.Errorf("the partial reading was lost: %q", n.Verbatim)
			}
		}
	}

	// Accepting without a decision is refused, and the refusal names the
	// element and quotes what the model could make out — that partial reading
	// is what a human needs in order to supply the real name.
	err := f.h.decodeErr(f.admin.do(http.MethodPost, "/api/v1/ingests/"+created.ID.String()+"/accept",
		map[string]any{"name": "Auth", "level": "context"}), http.StatusBadRequest)
	if err.Error.Code != "validation_failed" {
		t.Errorf("code = %q", err.Error.Code)
	}
	for _, want := range []string{unreadableSlug, "T?k?n St—", "resolutions"} {
		if !strings.Contains(err.Error.Message, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err.Error.Message)
		}
	}
	if err.Error.Details["unreadable"] == nil {
		t.Error("the refusal carries no machine-readable list of what to fix")
	}

	// Naming it makes it usable, and the provenance says a human read it.
	var accepted struct {
		DiagramID uuid.UUID `json:"diagram_id"`
	}
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/ingests/"+created.ID.String()+"/accept",
		map[string]any{
			"name": "Auth", "level": "context",
			"resolutions": []map[string]any{{"slug": unreadableSlug, "name": "Token Store"}},
		}), http.StatusCreated, &accepted)

	var view struct {
		Nodes []struct {
			Slug       string   `json:"slug"`
			Name       string   `json:"name"`
			Provenance string   `json:"provenance"`
			Confidence *float64 `json:"confidence"`
		} `json:"nodes"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/diagrams/"+accepted.DiagramID.String(), nil),
		http.StatusOK, &view)

	var seen bool
	for _, n := range view.Nodes {
		if n.Slug != "token-store" {
			continue
		}
		seen = true
		if n.Provenance != "human_edited" {
			t.Errorf("provenance = %q, want human_edited — a human read it", n.Provenance)
		}
		if n.Confidence != nil {
			t.Errorf("a human-supplied name must not carry the model's confidence: %v", *n.Confidence)
		}
	}
	if !seen {
		t.Errorf("the renamed box is not on the diagram: %+v", view.Nodes)
	}
}

func TestDroppingAnUnreadableBoxTakesItsArrowsWithIt(t *testing.T) {
	f := newIngestFixture(t)
	f.model.script(0, `{
	  "nodes": [
	    {"name":"Auth Service","kind":"service","confidence":0.9,"legible":true},
	    {"name":"smudge","kind":"datastore","confidence":0.1,"legible":false,"verbatim":"…"}
	  ],
	  "edges": [{"from":"Auth Service","to":"smudge","relation":"writes_to","confidence":0.7}]
	}`)

	created := f.upload(f.admin, 1)
	got := f.settle(f.admin, created.ID)

	var slug string
	for _, n := range got.Merged.Nodes {
		if !n.Legible {
			slug = n.Slug
		}
	}

	var accepted struct {
		DiagramID  uuid.UUID `json:"diagram_id"`
		NodesAdded int       `json:"nodes_added"`
		EdgesAdded int       `json:"edges_added"`
		Dropped    []string  `json:"dropped"`
	}
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/ingests/"+created.ID.String()+"/accept",
		map[string]any{
			"name": "Auth", "level": "context",
			"resolutions": []map[string]any{{"slug": slug, "drop": true}},
		}), http.StatusCreated, &accepted)

	if accepted.NodesAdded != 1 || accepted.EdgesAdded != 0 {
		t.Errorf("dropping a box must take its arrows with it: %+v", accepted)
	}
	if len(accepted.Dropped) != 1 || accepted.Dropped[0] != slug {
		t.Errorf("dropped = %v", accepted.Dropped)
	}
}

// TestAMalformedPageFailsThePageNotTheIngest is why extraction is per page.
func TestAMalformedPageFailsThePageNotTheIngest(t *testing.T) {
	f := newIngestFixture(t)
	f.model.script(0, `{"nodes":[{"name":"Auth Service","kind":"service","confidence":0.9,"legible":true}],"edges":[]}`)
	f.model.script(1, "I looked at the page and here is what I think it says:")

	created := f.upload(f.admin, 2)
	got := f.settle(f.admin, created.ID)

	// Four readable pages and one smudged one is a reviewable result, not a
	// dead upload.
	if got.Status != "review" {
		t.Fatalf("status = %q, want review — one bad page must not kill the ingest", got.Status)
	}
	if p := got.page(t, 0); p.Status != "extracted" {
		t.Errorf("the good page did not survive: %+v", p)
	}
	bad := got.page(t, 1)
	if bad.Status != "failed" {
		t.Fatalf("the malformed page is %q, want failed", bad.Status)
	}
	if bad.ErrorClass != "malformed_json" {
		t.Errorf("error_class = %q; the failure mode should be a GROUP BY, not a grep", bad.ErrorClass)
	}
	// One retry inside the call, with the teaching error, then the page fails.
	if bad.Attempts != 1 {
		t.Errorf("attempts = %d, want 1 extraction", bad.Attempts)
	}
	if n := len(f.model.callsFor(1)); n != 2 {
		t.Errorf("the bad page got %d model calls; want 2 — the second carries the correction", n)
	}

	if got.Merged == nil || len(got.Merged.PagesFailed) != 1 || got.Merged.PagesFailed[0] != 1 {
		t.Errorf("the merge does not record which page was lost: %+v", got.Merged)
	}
}

// TestAnAnswerWithNoBoxesIsAFailureNotAnEmptyDiagram is the bug the manual test
// found (D38). Ollama Cloud accepts `format` and discards it, so the model
// answered with three arrows, zero boxes, and a key it invented — and the
// decoder reported success over an extraction containing nothing.
func TestAnAnswerWithNoBoxesIsAFailureNotAnEmptyDiagram(t *testing.T) {
	f := newIngestFixture(t)
	f.model.script(0, `{"boxes":[{"name":"Auth Service","kind":"service"}],
	  "edges":[{"from":"browser","to":"auth_service","relation":"calls","confidence":0.9}]}`)

	created := f.upload(f.admin, 1)
	got := f.settle(f.admin, created.ID)

	// Every arrow starts and ends at a box, so a page with arrows has boxes.
	// An empty result and a successful one must never look the same.
	if got.Status != "failed" {
		t.Fatalf("status = %q; an extraction with no boxes is not a successful extraction", got.Status)
	}
	p := got.page(t, 0)
	if p.ErrorClass != "no_nodes" {
		t.Errorf("error_class = %q, want no_nodes", p.ErrorClass)
	}
	// The correction names the key, because inventing one is exactly what
	// happened.
	calls := f.model.callsFor(0)
	if len(calls) != 2 {
		t.Fatalf("want a corrective retry, got %d calls", len(calls))
	}
	correction, _ := json.Marshal(calls[1].messages)
	if !strings.Contains(string(correction), "`nodes` array is missing or empty") {
		t.Errorf("the correction does not name the problem:\n%s", correction)
	}
}

// TestTheModelsAnswerIsKeptVerbatim: `extracted` has been through JSONB and a Go
// struct, so it cannot answer "what did the model actually say" — which is the
// only question worth asking when an extraction is wrong.
func TestTheModelsAnswerIsKeptVerbatim(t *testing.T) {
	f := newIngestFixture(t)
	// A cloud model's answer: prose, a fenced block, and a stray key.
	good := "Here is what I could read:\n\n```json\n" +
		`{"nodes":[{"name":"Auth Service","kind":"service","confidence":0.9,"legible":true}],"edges":[],"unexpected":1}` +
		"\n```\nHope that helps."
	f.model.script(0, good)
	f.model.script(1, "I am afraid this page is completely illegible.")

	created := f.upload(f.admin, 2)
	got := f.settle(f.admin, created.ID)

	// The JSON is found inside the prose. That is normalising a wrapper, not
	// parsing around the failure mode: the answer still has to match the schema.
	ok := got.page(t, 0)
	if ok.Status != "extracted" {
		t.Fatalf("page 1 = %+v; the JSON was in there", ok)
	}
	if ok.Raw != good {
		t.Errorf("the answer was not kept verbatim:\n%q", ok.Raw)
	}

	// And on the failure, where you want it most.
	bad := got.page(t, 1)
	if bad.Status != "failed" {
		t.Fatalf("page 2 = %+v", bad)
	}
	if !strings.Contains(bad.Raw, "completely illegible") {
		t.Errorf("a rejected answer was not kept: %q", bad.Raw)
	}
}

func TestRetryIsPerPage(t *testing.T) {
	f := newIngestFixture(t)
	f.model.script(0, `{"nodes":[{"name":"Auth Service","kind":"service","confidence":0.9,"legible":true}],"edges":[]}`)
	f.model.script(1, "not json at all")

	created := f.upload(f.admin, 2)
	got := f.settle(f.admin, created.ID)
	if got.page(t, 1).Status != "failed" {
		t.Fatal("expected page 2 to fail first")
	}
	callsToGoodPage := len(f.model.callsFor(0))

	// The page was out of focus; the model is fine now.
	f.model.script(1, `{"nodes":[{"name":"Token Store","kind":"datastore","confidence":0.9,"legible":true}],"edges":[]}`)
	f.h.decode(f.admin.do(http.MethodPost,
		"/api/v1/ingests/"+created.ID.String()+"/pages/1/retry", nil), http.StatusAccepted, nil)

	retried := f.settle(f.admin, created.ID)
	if retried.page(t, 1).Status != "extracted" {
		t.Fatalf("the retried page is %+v", retried.page(t, 1))
	}
	// Re-running the whole ingest would cost a model call for every page and
	// throw away an extraction somebody has already reviewed.
	if got := len(f.model.callsFor(0)); got != callsToGoodPage {
		t.Errorf("the good page was re-read %d extra times", got-callsToGoodPage)
	}
	// The merge ran again over the new page.
	if len(retried.Merged.Nodes) != 2 || len(retried.Merged.PagesFailed) != 0 {
		t.Errorf("the merge was not re-run after the retry: %+v", retried.Merged)
	}
}

// TestExtractionIsConfinedToTheClosedVocabulary checks both halves: the model is
// taught the valid values, and a page that keeps ignoring them fails rather than
// having its answer quietly coerced.
func TestExtractionIsConfinedToTheClosedVocabulary(t *testing.T) {
	f := newIngestFixture(t)
	f.model.script(0,
		`{"nodes":[{"name":"Auth","kind":"microservice","confidence":0.9,"legible":true}],"edges":[]}`,
		`{"nodes":[{"name":"Auth","kind":"service","confidence":0.9,"legible":true}],"edges":[]}`,
	)
	f.model.script(1, `{"nodes":[],"edges":[{"from":"a","to":"b","relation":"talks_to","confidence":0.9}]}`)

	created := f.upload(f.admin, 2)
	got := f.settle(f.admin, created.ID)

	// Page 1 was corrected by being shown the vocabulary with its meanings.
	if p := got.page(t, 0); p.Status != "extracted" {
		t.Errorf("page 1 should have recovered on the second attempt: %+v", p)
	}
	calls := f.model.callsFor(0)
	if len(calls) != 2 {
		t.Fatalf("page 1 got %d calls, want 2", len(calls))
	}
	correction, _ := json.Marshal(calls[1].messages)
	for _, want := range []string{"is not a valid node kind", "an independently deployable process"} {
		if !strings.Contains(string(correction), want) {
			t.Errorf("the correction does not teach the vocabulary (%q missing):\n%s", want, correction)
		}
	}

	// Page 2 never corrected itself. Coercing `talks_to` to `calls` would be
	// right often enough to be trusted and wrong often enough to matter.
	bad := got.page(t, 1)
	if bad.Status != "failed" || bad.ErrorClass != "vocabulary" {
		t.Errorf("page 2 = %+v, want failed/vocabulary", bad)
	}
	if !strings.Contains(bad.ErrorDetail, "data_flow") {
		t.Errorf("the recorded failure should carry the teaching text: %q", bad.ErrorDetail)
	}

	// The schema is generated from the vocabulary, so the two cannot drift.
	schema := string(calls[0].format)
	for _, want := range []string{"external_system", "subscribes_to", "continues_off_page", "legible"} {
		if !strings.Contains(schema, want) {
			t.Errorf("the constrained format is missing %q", want)
		}
	}
	if calls[0].think == nil || *calls[0].think {
		t.Error("extraction should not run through the reasoning channel; that is where structured answers go to die")
	}
}

// TestAnUnplaceableArrowBecomesANoteNotAnInvention: a dropped dependency is the
// one error nobody notices.
func TestAnUnplaceableArrowBecomesANoteNotAnInvention(t *testing.T) {
	f := newIngestFixture(t)
	f.model.script(0, `{
	  "nodes": [{"name":"Auth Service","kind":"service","confidence":0.9,"legible":true}],
	  "edges": [{"from":"Auth Service","to":"Billing?","relation":"calls","confidence":0.6,"continues_off_page":true}]
	}`)

	created := f.upload(f.admin, 1)
	got := f.settle(f.admin, created.ID)
	if got.Counts.Unresolved != 1 {
		t.Fatalf("the dangling arrow was not surfaced: %+v", got.Counts)
	}

	var accepted struct {
		DiagramID  uuid.UUID `json:"diagram_id"`
		NodesAdded int       `json:"nodes_added"`
		EdgesAdded int       `json:"edges_added"`
		Notes      []struct {
			Kind string `json:"kind"`
			Body string `json:"body"`
		} `json:"notes"`
	}
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/ingests/"+created.ID.String()+"/accept",
		map[string]any{"name": "Auth", "level": "context"}), http.StatusCreated, &accepted)

	// Nothing was invented to receive it.
	if accepted.NodesAdded != 1 || accepted.EdgesAdded != 0 {
		t.Errorf("an unplaceable arrow must not conjure a node: %+v", accepted)
	}
	var noted bool
	for _, n := range accepted.Notes {
		if n.Kind == "missing" && strings.Contains(n.Body, "Billing?") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("the arrow vanished without a note: %+v", accepted.Notes)
	}
}

// TestAcceptIsAdditive: a photograph is evidence that these things exist, never
// evidence that everything else does not.
func TestAcceptIsAdditive(t *testing.T) {
	f := newIngestFixture(t)

	var existing struct {
		ID uuid.UUID `json:"id"`
	}
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/diagrams",
		map[string]any{"name": "Auth Context", "level": "context"}), http.StatusCreated, &existing)

	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/diagrams/"+existing.ID.String()+"/lease", nil),
		http.StatusOK, nil)
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/diagrams/"+existing.ID.String()+"/revisions",
		map[string]any{
			"base_revision": 0, "summary": "by hand",
			"nodes": []map[string]any{
				{"slug": "session-reaper", "kind": "job", "name": "Session Reaper"},
			},
			"edges": []map[string]any{},
		}), http.StatusCreated, nil)

	f.model.script(0, `{"nodes":[{"name":"Auth Service","kind":"service","confidence":0.9,"legible":true}],"edges":[]}`)
	created := f.upload(f.admin, 1)
	f.settle(f.admin, created.ID)

	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/ingests/"+created.ID.String()+"/accept",
		map[string]any{"diagram_id": existing.ID.String()}), http.StatusCreated, nil)

	var view struct {
		Nodes []struct {
			Slug string `json:"slug"`
		} `json:"nodes"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/api/v1/diagrams/"+existing.ID.String(), nil),
		http.StatusOK, &view)

	slugs := map[string]bool{}
	for _, n := range view.Nodes {
		slugs[n.Slug] = true
	}
	if !slugs["session-reaper"] {
		t.Error("accepting a drawing deleted a node the drawing did not happen to include")
	}
	if !slugs["auth-service"] {
		t.Error("the extracted node was not applied")
	}
}

func TestAnIngestIsAcceptedOnlyOnce(t *testing.T) {
	f := newIngestFixture(t)
	f.model.script(0, `{"nodes":[{"name":"Auth Service","kind":"service","confidence":0.9,"legible":true}],"edges":[]}`)

	created := f.upload(f.admin, 1)
	f.settle(f.admin, created.ID)

	body := map[string]any{"name": "Auth", "level": "context"}
	f.h.decode(f.admin.do(http.MethodPost, "/api/v1/ingests/"+created.ID.String()+"/accept", body),
		http.StatusCreated, nil)

	err := f.h.decodeErr(f.admin.do(http.MethodPost, "/api/v1/ingests/"+created.ID.String()+"/accept", body),
		http.StatusConflict)
	if !strings.Contains(err.Error.Message, "accepted") {
		t.Errorf("the second accept should say the ingest is already accepted: %q", err.Error.Message)
	}
}

// TestADeveloperMayUploadButNotAccept: a photograph binds nobody to anything;
// applying it to the architecture is the write, and the whole diagram is
// manager-owned (§4.2).
func TestADeveloperMayUploadButNotAccept(t *testing.T) {
	f := newIngestFixture(t)
	f.model.script(0, `{"nodes":[{"name":"Auth Service","kind":"service","confidence":0.9,"legible":true}],"edges":[]}`)

	created := f.upload(f.dev, 1)
	f.settle(f.dev, created.ID)

	err := f.h.decodeErr(f.dev.do(http.MethodPost, "/api/v1/ingests/"+created.ID.String()+"/accept",
		map[string]any{"name": "Auth", "level": "context"}), http.StatusForbidden)
	if !strings.Contains(err.Error.Message, "developer") {
		t.Errorf("the refusal should name the caller's effective role: %q", err.Error.Message)
	}
}

func TestOneIngestIsOneDocument(t *testing.T) {
	f := newIngestFixture(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	img, _ := w.CreateFormFile("files", "page.png")
	_, _ = img.Write(pageImage(t, 0))
	pdf, _ := w.CreateFormFile("files", "doc.pdf")
	_, _ = pdf.Write([]byte("%PDF-1.4\n%%EOF\n"))
	_ = w.Close()

	req, _ := http.NewRequest(http.MethodPost,
		f.h.srv.URL+"/api/v1/projects/"+f.pid.String()+"/ingests", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(f.admin.cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	got := f.h.decodeErr(resp, http.StatusBadRequest)
	// Merging across two documents would invent cross-references between
	// drawings that were never on the same desk.
	if !strings.Contains(got.Error.Message, "separately") {
		t.Errorf("the refusal should say what to do instead: %q", got.Error.Message)
	}
}

func TestTheIngestSurfaceMatchesTheContract(t *testing.T) {
	f := newIngestFixture(t)

	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	f.h.decode(f.admin.do(http.MethodGet, "/openapi.json", nil), http.StatusOK, &doc)

	for _, want := range []struct{ path, method string }{
		{"/api/v1/projects/{pid}/ingests", "post"},
		{"/api/v1/projects/{pid}/ingests", "get"},
		{"/api/v1/ingests/{id}", "get"},
		{"/api/v1/ingests/{id}/accept", "post"},
		{"/api/v1/ingests/{id}/pages/{idx}/retry", "post"},
		{"/api/v1/ingests/{id}/pages/{idx}/image", "get"},
	} {
		item, ok := doc.Paths[want.path]
		if !ok {
			t.Errorf("%s is not in the OpenAPI document", want.path)
			continue
		}
		if _, ok := item[want.method]; !ok {
			t.Errorf("%s %s is not in the OpenAPI document", strings.ToUpper(want.method), want.path)
		}
	}
}
