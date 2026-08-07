package ingest

import (
	"encoding/json"
	"strings"
	"testing"

	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
)

// The merge step is where the "continues on the next page" arrow either works
// or silently loses a dependency, so it is tested against hand-written pages
// rather than against a photograph: a failure here should point at a rule, not
// at a model having a bad day.

func page(idx int, e *PageExtraction) *Page {
	return &Page{Idx: idx, Status: PageExtracted, Extracted: e}
}

func node(name string, kind dg.NodeKind, conf float32) ExtractedNode {
	return ExtractedNode{Name: name, Kind: kind, Confidence: conf, Legible: true}
}

func edge(from, to string, rel dg.Relation, conf float32) ExtractedEdge {
	return ExtractedEdge{From: from, To: to, Relation: rel, Confidence: conf}
}

func TestTheSameBoxOnTwoPagesIsOneBox(t *testing.T) {
	m := Merge([]*Page{
		page(0, &PageExtraction{
			Nodes: []ExtractedNode{node("Auth Service", dg.KindService, 0.9)},
		}),
		page(1, &PageExtraction{
			// Written differently, normalising to the same name.
			Nodes: []ExtractedNode{node("auth  service", dg.KindService, 0.5)},
		}),
	})

	if len(m.Nodes) != 1 {
		t.Fatalf("want one merged node, got %d: %+v", len(m.Nodes), m.Nodes)
	}
	n := m.Nodes[0]
	if n.Slug != "auth-service" {
		t.Errorf("slug = %q, want auth-service", n.Slug)
	}
	// The weakest reading governs: a box read clearly on page 1 and barely on
	// page 2 is a box worth a second look, and averaging hides the bad page.
	if n.Confidence != 0.5 {
		t.Errorf("confidence = %v, want the weakest reading 0.5", n.Confidence)
	}
	if len(n.Pages) != 2 {
		t.Errorf("pages = %v, want both", n.Pages)
	}
	if !n.NeedsReview {
		t.Error("0.5 is below the review threshold and should be flagged")
	}
}

func TestPagesThatDisagreeAboutAKindAreRecordedNotResolved(t *testing.T) {
	m := Merge([]*Page{
		page(0, &PageExtraction{Nodes: []ExtractedNode{node("Auth Service", dg.KindService, 0.9)}}),
		page(1, &PageExtraction{Nodes: []ExtractedNode{node("Auth Service", dg.KindComponent, 0.7)}}),
	})

	if len(m.Nodes) != 1 {
		t.Fatalf("a disagreement about kind is still one box, got %d", len(m.Nodes))
	}
	if m.Nodes[0].Kind != dg.KindService {
		t.Errorf("kind = %s, want the most confident reading (service)", m.Nodes[0].Kind)
	}
	if !m.Nodes[0].NeedsReview {
		t.Error("a kind disagreement must be flagged for review")
	}
	if len(m.Conflicts) != 1 {
		t.Fatalf("want one recorded conflict, got %d", len(m.Conflicts))
	}
	c := m.Conflicts[0]
	if c.Kind != "node_kind_disagreement" {
		t.Errorf("conflict kind = %q", c.Kind)
	}
	// The detail is read by a human and by a model. It has to say which pages
	// said what, or it is not actionable.
	for _, want := range []string{"service on page 1", "component on page 2", "Auth Service"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("conflict detail does not mention %q:\n%s", want, c.Detail)
		}
	}
}

func TestAMostConfidentKindIsNotBeatenByALaterMediocreOne(t *testing.T) {
	// Regression: the merged node's confidence is the *weakest* reading, so
	// comparing a new reading against it lets page 3's 0.6 overwrite page 1's
	// 0.9 the moment page 2 dragged the minimum down to 0.4.
	m := Merge([]*Page{
		page(0, &PageExtraction{Nodes: []ExtractedNode{node("Store", dg.KindDatastore, 0.9)}}),
		page(1, &PageExtraction{Nodes: []ExtractedNode{node("Store", dg.KindDatastore, 0.4)}}),
		page(2, &PageExtraction{Nodes: []ExtractedNode{node("Store", dg.KindQueue, 0.6)}}),
	})
	if m.Nodes[0].Kind != dg.KindDatastore {
		t.Errorf("kind = %s, want datastore from the 0.9 reading", m.Nodes[0].Kind)
	}
	if m.Nodes[0].Confidence != 0.4 {
		t.Errorf("confidence = %v, want the weakest 0.4", m.Nodes[0].Confidence)
	}
}

func TestAnUnreadableBoxIsNeverMergedWithAnything(t *testing.T) {
	m := Merge([]*Page{
		page(0, &PageExtraction{Nodes: []ExtractedNode{
			{Name: "box below the queue", Kind: dg.KindService, Confidence: 0.2, Legible: false, Verbatim: "Au?h S…"},
		}}),
		page(1, &PageExtraction{Nodes: []ExtractedNode{
			{Name: "box below the queue", Kind: dg.KindService, Confidence: 0.2, Legible: false},
		}}),
	})

	// Two pages cannot accidentally agree that "box below the queue" is the
	// same box. The only honest reading is one unread box on each page.
	if len(m.Nodes) != 2 {
		t.Fatalf("want two separate unreadable boxes, got %d: %+v", len(m.Nodes), m.Nodes)
	}
	for _, n := range m.Nodes {
		if n.Legible {
			t.Errorf("%s should be illegible", n.Slug)
		}
		if !n.NeedsReview || n.ReviewReason == "" {
			t.Errorf("%s must be flagged with a reason", n.Slug)
		}
		if !strings.HasPrefix(n.Slug, "unreadable-p") {
			t.Errorf("slug = %q, want a positional placeholder rather than a slugified guess", n.Slug)
		}
	}
	if m.Nodes[0].Verbatim != "Au?h S…" {
		t.Errorf("the partial reading was lost: %q", m.Nodes[0].Verbatim)
	}
	if got := m.Counts().Illegible; got != 2 {
		t.Errorf("Counts().Illegible = %d, want 2", got)
	}
}

func TestAnArrowThatContinuesOntoAnotherPageIsJoinedAndFlagged(t *testing.T) {
	m := Merge([]*Page{
		page(0, &PageExtraction{
			Nodes: []ExtractedNode{node("Auth Service", dg.KindService, 0.9)},
			Edges: []ExtractedEdge{{
				From: "Auth Service", To: "Token Store", Relation: dg.RelWritesTo,
				Confidence: 0.8, ContinuesOffPage: true,
			}},
		}),
		page(1, &PageExtraction{
			Nodes: []ExtractedNode{node("Token Store", dg.KindDatastore, 0.9)},
		}),
	})

	if len(m.Edges) != 1 {
		t.Fatalf("want the cross-page arrow, got %d edges", len(m.Edges))
	}
	e := m.Edges[0]
	if e.From != "auth-service" || e.To != "token-store" {
		t.Errorf("endpoints = %s -> %s, want both resolved", e.From, e.To)
	}
	if e.Unresolved {
		t.Error("both ends were read; this arrow is not unresolved")
	}
	// Joined, and flagged. This is the merge doing its actual work, and it is
	// where a reviewer should look hardest.
	if !e.CrossPage || !e.NeedsReview {
		t.Errorf("a cross-page join must be flagged: %+v", e)
	}
}

func TestAnArrowIntoNothingIsKeptRatherThanDropped(t *testing.T) {
	m := Merge([]*Page{
		page(0, &PageExtraction{
			Nodes: []ExtractedNode{node("Auth Service", dg.KindService, 0.9)},
			Edges: []ExtractedEdge{{
				From: "Auth Service", To: "Billing?", Relation: dg.RelCalls,
				Confidence: 0.6, ContinuesOffPage: true,
			}},
		}),
	})

	if len(m.Edges) != 1 {
		t.Fatalf("a dangling arrow must survive the merge; got %d edges", len(m.Edges))
	}
	e := m.Edges[0]
	if !e.Unresolved || !e.NeedsReview {
		t.Errorf("want unresolved and flagged, got %+v", e)
	}
	if !strings.Contains(e.ReviewReason, "Billing?") {
		t.Errorf("the reason must quote what the model read: %q", e.ReviewReason)
	}
	if got := m.Counts().Unresolved; got != 1 {
		t.Errorf("Counts().Unresolved = %d, want 1", got)
	}
}

func TestPagesThatDisagreeAboutARelationAreRecorded(t *testing.T) {
	nodes := []ExtractedNode{
		node("Auth Service", dg.KindService, 0.9),
		node("Token Store", dg.KindDatastore, 0.9),
	}
	m := Merge([]*Page{
		page(0, &PageExtraction{Nodes: nodes, Edges: []ExtractedEdge{
			edge("Auth Service", "Token Store", dg.RelWritesTo, 0.9)}}),
		page(1, &PageExtraction{Nodes: nodes, Edges: []ExtractedEdge{
			edge("Auth Service", "Token Store", dg.RelReadsFrom, 0.7)}}),
	})

	if len(m.Edges) != 1 {
		t.Fatalf("want one edge, got %d", len(m.Edges))
	}
	if m.Edges[0].Relation != dg.RelWritesTo {
		t.Errorf("relation = %s, want the most confident reading", m.Edges[0].Relation)
	}
	if len(m.Conflicts) != 1 || m.Conflicts[0].Kind != "relation_disagreement" {
		t.Fatalf("want a recorded relation disagreement, got %+v", m.Conflicts)
	}
}

func TestContainmentSurvivesTheMergeAndAWrongParentIsRefused(t *testing.T) {
	m := Merge([]*Page{page(0, &PageExtraction{Nodes: []ExtractedNode{
		{Name: "Trust Zone", Kind: dg.KindBoundary, Confidence: 0.9, Legible: true},
		{Name: "Auth Service", Kind: dg.KindService, Confidence: 0.9, Legible: true, Parent: "Trust Zone"},
		{Name: "Token Store", Kind: dg.KindDatastore, Confidence: 0.9, Legible: true, Parent: "Auth Service"},
		{Name: "Reaper", Kind: dg.KindJob, Confidence: 0.9, Legible: true, Parent: "Nowhere"},
	}})})

	byslug := map[string]MergedNode{}
	for _, n := range m.Nodes {
		byslug[n.Slug] = n
	}
	if got := byslug["auth-service"].Parent; got != "trust-zone" {
		t.Errorf("auth-service parent = %q, want trust-zone", got)
	}
	// Only a boundary or a system may contain. A datastore inside a service is
	// a misreading, and it is surfaced rather than carried into a diagram that
	// would then fail validation.
	if p := byslug["token-store"]; p.Parent != "" || !p.NeedsReview {
		t.Errorf("a service cannot contain: %+v", p)
	}
	if p := byslug["reaper"]; p.Parent != "" || !strings.Contains(p.ReviewReason, "Nowhere") {
		t.Errorf("a parent that names nothing must be surfaced: %+v", p)
	}
}

func TestAFailedPageIsRecordedAndDoesNotStopTheMerge(t *testing.T) {
	m := Merge([]*Page{
		page(0, &PageExtraction{Nodes: []ExtractedNode{node("Auth Service", dg.KindService, 0.9)}}),
		{Idx: 1, Status: PageFailed, ErrorClass: "malformed_json"},
		page(2, &PageExtraction{Nodes: []ExtractedNode{node("Token Store", dg.KindDatastore, 0.9)}}),
	})

	if len(m.Nodes) != 2 {
		t.Fatalf("the readable pages must still merge, got %d nodes", len(m.Nodes))
	}
	if len(m.PagesRead) != 2 || len(m.PagesFailed) != 1 || m.PagesFailed[0] != 1 {
		t.Errorf("read = %v, failed = %v", m.PagesRead, m.PagesFailed)
	}
}

func TestTheMergeDoesNotDependOnPageArrivalOrder(t *testing.T) {
	// Pages finish in whatever order the model returns them. A payload that
	// shuffles between runs cannot be diffed, cannot be reviewed twice, and
	// makes "what did I approve" unanswerable.
	pages := []*Page{
		page(0, &PageExtraction{
			Nodes: []ExtractedNode{node("Zeta", dg.KindService, 0.9), node("Alpha", dg.KindService, 0.8)},
			Edges: []ExtractedEdge{edge("Zeta", "Alpha", dg.RelCalls, 0.9)},
		}),
		page(1, &PageExtraction{
			Nodes: []ExtractedNode{node("Middle", dg.KindQueue, 0.7), node("Alpha", dg.KindService, 0.6)},
			Edges: []ExtractedEdge{edge("Alpha", "Middle", dg.RelPublishesTo, 0.7)},
		}),
	}

	forward, err := json.Marshal(Merge(pages))
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := json.Marshal(Merge([]*Page{pages[1], pages[0]}))
	if err != nil {
		t.Fatal(err)
	}
	if string(forward) != string(reversed) {
		t.Errorf("the merge is order-dependent:\n%s\n%s", forward, reversed)
	}
}

func TestAnEmptyMergeIsStillAValidPayload(t *testing.T) {
	m := Merge(nil)
	if m.Nodes == nil || m.Edges == nil || m.PagesRead == nil {
		t.Errorf("the empty payload must serialize as empty lists, not nulls: %+v", m)
	}
}
