package diagram

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
)

// compose is the whole meaning of "apply a diagram proposal", and it is pure —
// so the rules that decide what a proposal *does* are testable with no database
// and no model, which is the same argument D37 makes for the ingest merge.

func graph(nodes []string, kinds []dg.NodeKind, edges [][3]string) *dg.Graph {
	g := &dg.Graph{}
	ids := map[string]uuid.UUID{}
	for i, slug := range nodes {
		id := uuid.Must(uuid.NewV7())
		ids[slug] = id
		g.Nodes = append(g.Nodes, &dg.Node{
			ID: id, Provenance: dg.ProvHumanCreated,
			Entity: &dg.Entity{ID: uuid.Must(uuid.NewV7()), Slug: slug, Kind: kinds[i], Name: slug},
		})
	}
	for _, e := range edges {
		g.Edges = append(g.Edges, &dg.Edge{
			ID: uuid.Must(uuid.NewV7()), FromNodeID: ids[e[0]], ToNodeID: ids[e[2]],
			Relation: dg.Relation(e[1]), Provenance: dg.ProvHumanCreated,
		})
	}
	return g
}

func slugsOf(in []NodeInput) []string {
	out := make([]string, 0, len(in))
	for _, n := range in {
		out = append(out, n.Slug)
	}
	sort.Strings(out)
	return out
}

func keysOf(in []EdgeInput) []string {
	out := make([]string, 0, len(in))
	for _, e := range in {
		out = append(out, e.From+" "+string(e.Relation)+" "+e.To)
	}
	sort.Strings(out)
	return out
}

func node(op EditOp, in NodeInput) GraphEdit { return GraphEdit{Op: op, Node: &in} }
func edge(op EditOp, in EdgeInput) GraphEdit { return GraphEdit{Op: op, Edge: &in} }

func base() *dg.Graph {
	return graph(
		[]string{"browser", "auth-service", "token-store"},
		[]dg.NodeKind{dg.KindActor, dg.KindService, dg.KindDatastore},
		[][3]string{{"browser", "calls", "auth-service"}, {"auth-service", "writes_to", "token-store"}},
	)
}

// TestDeletingANodeTakesItsArrows is the rule that would otherwise be found
// after the write, as a graph-validation failure with nothing to point at.
func TestDeletingANodeTakesItsArrows(t *testing.T) {
	out, err := compose(base(), []GraphEdit{node(EditDelete, NodeInput{Slug: "token-store"})})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(slugsOf(out.nodes), ","); got != "auth-service,browser" {
		t.Errorf("nodes = %s, want auth-service,browser", got)
	}
	if got := strings.Join(keysOf(out.edges), ","); got != "browser calls auth-service" {
		t.Errorf("edges = %s; the arrow into the deleted box survived", got)
	}
}

// TestDeletingABoundaryDoesNotDeleteWhatWasInside: containment is a
// relationship, not ownership of the contents.
func TestDeletingABoundaryDoesNotDeleteWhatWasInside(t *testing.T) {
	g := graph(
		[]string{"internal", "auth-service"},
		[]dg.NodeKind{dg.KindBoundary, dg.KindService},
		nil,
	)
	g.Nodes[1].ParentSlug = "internal"

	out, err := compose(g, []GraphEdit{node(EditDelete, NodeInput{Slug: "internal"})})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.nodes) != 1 || out.nodes[0].Slug != "auth-service" {
		t.Fatalf("nodes = %v, want just auth-service", slugsOf(out.nodes))
	}
	if out.nodes[0].Parent != "" {
		t.Errorf("auth-service still names %q as its parent, which is not in the diagram", out.nodes[0].Parent)
	}
}

// TestAnUpdateOnlyTouchesTheFieldsItNames. A proposal that changes a kind must
// not blank the summary it never mentioned — that is a silent data loss dressed
// up as an approved change.
func TestAnUpdateOnlyTouchesTheFieldsItNames(t *testing.T) {
	g := base()
	g.Nodes[2].Entity.Summary = "session rows, 30 day retention"

	out, err := compose(g, []GraphEdit{node(EditUpdate, NodeInput{Slug: "token-store", Kind: dg.KindQueue})})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range out.nodes {
		if n.Slug != "token-store" {
			continue
		}
		if n.Kind != dg.KindQueue {
			t.Errorf("kind = %q, want queue", n.Kind)
		}
		if n.Summary != "session rows, 30 day retention" {
			t.Errorf("summary = %q; a change that named only `kind` blanked it", n.Summary)
		}
		if n.Name != "token-store" {
			t.Errorf("name = %q; a change that named only `kind` blanked it", n.Name)
		}
	}
}

// TestCreatingSomethingThatAlreadyExistsIsNotAnError. A proposal saying "add a
// Redis cache" against a diagram that has since gained one has got what it
// asked for, and failing the whole set over that is pedantry.
func TestCreatingSomethingThatAlreadyExistsIsNotAnError(t *testing.T) {
	out, err := compose(base(), []GraphEdit{
		node(EditCreate, NodeInput{Slug: "token-store", Kind: dg.KindDatastore, Summary: "added by the proposal"}),
	})
	if err != nil {
		t.Fatalf("re-creating an existing node failed: %v", err)
	}
	if len(out.nodes) != 3 {
		t.Errorf("nodes = %v, want the same three", slugsOf(out.nodes))
	}
	for _, n := range out.nodes {
		if n.Slug == "token-store" && n.Summary != "added by the proposal" {
			t.Errorf("the create's fields were discarded: summary = %q", n.Summary)
		}
	}
}

// TestDeletingSomethingAlreadyGoneIsNotAnError, for the same reason from the
// other direction.
func TestDeletingSomethingAlreadyGoneIsNotAnError(t *testing.T) {
	out, err := compose(base(), []GraphEdit{
		node(EditDelete, NodeInput{Slug: "session-cache"}),
		edge(EditDelete, EdgeInput{From: "browser", To: "token-store", Relation: dg.RelCalls}),
	})
	if err != nil {
		t.Fatalf("deleting what is already gone failed: %v", err)
	}
	if len(out.nodes) != 3 || len(out.edges) != 2 {
		t.Errorf("graph = %v / %v, want it untouched", slugsOf(out.nodes), keysOf(out.edges))
	}
}

// TestUpdatingSomethingAbsentIsRefusedAndSaysWhy — the case that must not
// silently become a create.
func TestUpdatingSomethingAbsentIsRefusedAndSaysWhy(t *testing.T) {
	_, err := compose(base(), []GraphEdit{node(EditUpdate, NodeInput{Slug: "session-cache", Kind: dg.KindQueue})})
	if err == nil {
		t.Fatal("updating a node that is not there succeeded")
	}
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("err = %T, want something that reads as a conflict", err)
	}
	if !strings.Contains(err.Error(), "session-cache") {
		t.Errorf("the failure does not name the element: %v", err)
	}
}

// TestAnEdgeToNowhereIsRefusedBeforeItIsWritten. Left to the write it would
// surface as a graph-validation failure after the rows had been inserted, which
// is the same outcome with none of the explanation.
func TestAnEdgeToNowhereIsRefusedBeforeItIsWritten(t *testing.T) {
	_, err := compose(base(), []GraphEdit{
		edge(EditCreate, EdgeInput{From: "auth-service", To: "session-cache", Relation: dg.RelReadsFrom}),
	})
	if err == nil {
		t.Fatal("an edge to a node that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "session-cache") {
		t.Errorf("the failure does not name the missing end: %v", err)
	}
}

// TestACreatedNodeCanBeTheEndOfACreatedEdge: the order inside one proposal is
// the order it was written in, and a box created by change 1 is available to
// change 2.
func TestACreatedNodeCanBeTheEndOfACreatedEdge(t *testing.T) {
	out, err := compose(base(), []GraphEdit{
		node(EditCreate, NodeInput{Slug: "session-cache", Kind: dg.KindDatastore, Name: "Session Cache"}),
		edge(EditCreate, EdgeInput{From: "auth-service", To: "session-cache", Relation: dg.RelReadsFrom}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.nodes) != 4 {
		t.Errorf("nodes = %v, want four", slugsOf(out.nodes))
	}
	if got := keysOf(out.edges); len(got) != 3 {
		t.Errorf("edges = %v, want three", got)
	}
}
