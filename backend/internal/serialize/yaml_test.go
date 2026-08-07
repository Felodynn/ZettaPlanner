package serialize_test

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/serialize"
)

// builder assembles a graph in a controllable order, so the same logical graph
// can be built two different ways and compared byte for byte.
type builder struct {
	g   *diagram.Graph
	ids map[string]uuid.UUID
}

func newBuilder(level diagram.Level) *builder {
	return &builder{
		g: &diagram.Graph{Diagram: &diagram.Diagram{
			ID: uuid.Must(uuid.NewV7()), Slug: "auth-context", Name: "Auth Context", Level: level,
		}},
		ids: map[string]uuid.UUID{},
	}
}

func (b *builder) node(slug string, kind diagram.NodeKind, name, summary string, attrs map[string]any) *builder {
	id := uuid.Must(uuid.NewV7())
	b.ids[slug] = id
	b.g.Nodes = append(b.g.Nodes, &diagram.Node{
		ID:         id,
		DiagramID:  b.g.Diagram.ID,
		EntityID:   uuid.Must(uuid.NewV7()),
		Provenance: diagram.ProvHumanCreated,
		Entity: &diagram.Entity{
			Kind: kind, Name: name, Slug: slug, Summary: summary, Attrs: attrs,
		},
	})
	return b
}

func (b *builder) edge(from, to string, rel diagram.Relation, label string) *builder {
	b.g.Edges = append(b.g.Edges, &diagram.Edge{
		ID:         uuid.Must(uuid.NewV7()),
		DiagramID:  b.g.Diagram.ID,
		FromNodeID: b.ids[from],
		ToNodeID:   b.ids[to],
		Relation:   rel,
		Label:      label,
		Provenance: diagram.ProvHumanCreated,
	})
	return b
}

func (b *builder) note(subject string, kind diagram.NoteKind, body string) *builder {
	b.g.Notes = append(b.g.Notes, &diagram.Note{
		ID: uuid.Must(uuid.NewV7()), Kind: kind, Body: body,
		Status: diagram.NoteOpen, SubjectSlug: subject,
	})
	return b
}

// example is the graph from CONTRACT §6.2, plus enough extra to exercise the
// ordering rules.
func example(order string) *diagram.Graph {
	b := newBuilder(diagram.LevelContext)
	switch order {
	case "forward":
		b.node("auth-service", diagram.KindService, "Auth Service",
			"Issues and validates session tokens", map[string]any{"language": "go"})
		b.node("token-store", diagram.KindDatastore, "Token Store", "", nil)
		b.node("browser", diagram.KindActor, "Browser", "", nil)
		b.edge("auth-service", "token-store", diagram.RelWritesTo, "session rows")
		b.edge("browser", "auth-service", diagram.RelCalls, "POST /auth/token")
		b.note("token-store", diagram.NoteOverlap,
			`May be the same store as "session-db" in the container diagram. Unresolved.`)
	case "reverse":
		// The same graph, inserted backwards. Byte-identical output is the
		// property that makes an empty diff mean "nothing changed".
		b.node("browser", diagram.KindActor, "Browser", "", nil)
		b.node("token-store", diagram.KindDatastore, "Token Store", "", nil)
		b.node("auth-service", diagram.KindService, "Auth Service",
			"Issues and validates session tokens", map[string]any{"language": "go"})
		b.edge("browser", "auth-service", diagram.RelCalls, "POST /auth/token")
		b.edge("auth-service", "token-store", diagram.RelWritesTo, "session rows")
		b.note("token-store", diagram.NoteOverlap,
			`May be the same store as "session-db" in the container diagram. Unresolved.`)
	}
	return b.g
}

// TestSerializationIsStable: serialize, change nothing, serialize again.
func TestSerializationIsStable(t *testing.T) {
	g := example("forward")

	first, err := serialize.Graph(g)
	if err != nil {
		t.Fatal(err)
	}
	second, err := serialize.Graph(g)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("serializing the same graph twice produced different bytes:\n%s\n---\n%s", first, second)
	}
}

// TestSerializationIgnoresInsertionOrder is the harder half of determinism: the
// same graph, built in a different order, must produce the same bytes. Ordering
// by id would pass the previous test and fail this one, because a UUIDv7 is
// creation order.
func TestSerializationIgnoresInsertionOrder(t *testing.T) {
	forward, err := serialize.Graph(example("forward"))
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := serialize.Graph(example("reverse"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(forward, reverse) {
		t.Errorf("insertion order changed the bytes:\n--- forward ---\n%s\n--- reverse ---\n%s", forward, reverse)
	}
}

// TestAttrKeyOrderIsStable: Go map iteration is randomised per run, so an attrs
// map emitted in map order would produce different bytes on different runs of
// the same binary.
func TestAttrKeyOrderIsStable(t *testing.T) {
	attrs := map[string]any{
		"zebra": "z", "alpha": "a", "mango": "m", "delta": "d", "kilo": "k",
	}
	b := newBuilder(diagram.LevelContainer)
	b.node("thing", diagram.KindService, "Thing", "", attrs)

	want, err := serialize.Graph(b.g)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		got, err := serialize.Graph(b.g)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("attrs key order is not stable on run %d:\n%s\n---\n%s", i, want, got)
		}
	}
	if !strings.Contains(string(want), "alpha: a\n      delta: d\n      kilo: k\n      mango: m\n      zebra: z") {
		t.Errorf("attrs are not sorted:\n%s", want)
	}
}

// TestNoCoordinatesInOutput is the D5 guarantee, checked at the output as well
// as held by construction. The type has no coordinate field, so this can only
// fail if somebody adds one.
func TestNoCoordinatesInOutput(t *testing.T) {
	out, err := serialize.Graph(example("forward"))
	if err != nil {
		t.Fatal(err)
	}
	forbidden := regexp.MustCompile(`(?i)\b(x|y|w|h|width|height|waypoints?|collapsed|unplaced|position|layout)\s*:`)
	if m := forbidden.FindString(string(out)); m != "" {
		t.Errorf("the serialized diagram contains presentation data (%q):\n%s", m, out)
	}
}

// TestNumbersAreNormalised: JSONB round-trips every number through float64, so
// an attribute written as 3 comes back as 3.0. Emitting 3.0 would make a diff
// appear the first time a row is re-read, having changed nothing.
func TestNumbersAreNormalised(t *testing.T) {
	b := newBuilder(diagram.LevelComponent)
	b.node("thing", diagram.KindService, "Thing", "", map[string]any{
		"replicas": float64(3),   // arrived from JSONB as a float
		"cpu":      float64(0.5), // genuinely fractional
		"retries":  3,            // arrived as a Go int
		"enabled":  true,
		"unset":    nil,
		"tags":     []any{"b", "a"},
	})
	out, err := serialize.Graph(b.g)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)

	for _, want := range []string{"replicas: 3\n", "cpu: 0.5\n", "retries: 3\n", "enabled: true\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "replicas: 3.0") {
		t.Errorf("an integral float was emitted as a float:\n%s", body)
	}
	// A list keeps its own order: it is data, not a set the serializer may sort.
	if !strings.Contains(body, "- b\n") || strings.Index(body, "- b") > strings.Index(body, "- a") {
		t.Errorf("list order was not preserved:\n%s", body)
	}
}

// TestShapeMatchesTheContract checks the emitted document against the example
// in CONTRACT §6.2. The field order is part of the bytes, so it is part of the
// test.
func TestShapeMatchesTheContract(t *testing.T) {
	out, err := serialize.Graph(example("forward"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)

	if !strings.HasPrefix(body, "diagram: auth-context\nlevel: context\n") {
		t.Errorf("the document does not open with the diagram and its level:\n%s", body)
	}
	for _, section := range []string{"\nnodes:\n", "\nedges:\n", "\nnotes:\n"} {
		if !strings.Contains(body, section) {
			t.Errorf("missing section %q:\n%s", strings.TrimSpace(section), body)
		}
	}
	// Nodes are ordered by kind then slug: actor before datastore before
	// service.
	iActor := strings.Index(body, "id: browser")
	iStore := strings.Index(body, "id: token-store")
	iSvc := strings.Index(body, "id: auth-service")
	if !(iActor < iStore && iStore < iSvc) {
		t.Errorf("nodes are not ordered by kind then slug (actor, datastore, service):\n%s", body)
	}
	if !strings.Contains(body, "relation: writes_to") || !strings.Contains(body, "label: session rows") {
		t.Errorf("the edge did not serialize as expected:\n%s", body)
	}
}

// TestDeletedElementsAreOmitted: a soft-deleted node is history, not state, and
// must not reach a model as though it were still there.
func TestDeletedElementsAreOmitted(t *testing.T) {
	g := example("forward")
	now := g.Diagram.CreatedAt
	g.Nodes[1].DeletedAt = &now // token-store

	out, err := serialize.Graph(g)
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	if strings.Contains(body, "token-store") && !strings.Contains(body, "subject: token-store") {
		t.Errorf("a deleted node still appears:\n%s", body)
	}
	// Its edges go with it, rather than dangling at a node that is not there.
	if strings.Contains(body, "relation: writes_to") {
		t.Errorf("an edge to a deleted node survived:\n%s", body)
	}
}

// TestResolvedNotesAreOmitted: a resolved note is settled, and carrying it into
// every prompt spends context re-litigating it.
func TestResolvedNotesAreOmitted(t *testing.T) {
	g := example("forward")
	g.Notes[0].Status = diagram.NoteResolved

	out, err := serialize.Graph(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "notes:") {
		t.Errorf("a resolved note was serialized:\n%s", out)
	}
}

// TestPathsAreDerivedFromSlugs: a rename must not look like a delete plus an
// add in the git projection (§6.2).
func TestPathsAreDerivedFromSlugs(t *testing.T) {
	g := example("forward")
	node := g.Nodes[0]

	before := serialize.NodePath(node)
	node.Entity.Name = "Authentication Service (v2)"
	node.LabelOverride = "Renamed On This Diagram"
	after := serialize.NodePath(node)

	if before != after {
		t.Errorf("renaming changed the path: %q -> %q", before, after)
	}
	if before != "nodes/auth-service.yaml" {
		t.Errorf("path = %q, want nodes/auth-service.yaml", before)
	}

	from, to := g.NodeByID(g.Edges[0].FromNodeID), g.NodeByID(g.Edges[0].ToNodeID)
	if got := serialize.EdgePath(from, to, g.Edges[0].Relation); got != "edges/auth-service__writes_to__token-store.yaml" {
		t.Errorf("edge path = %q", got)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Auth Service":      "auth-service",
		"  Token   Store  ": "token-store",
		"API/Gateway (v2)":  "api-gateway-v2",
		"Postgres 16":       "postgres-16",
		"---":               "",
		"Zetta—Planner":     "zetta-planner",
	}
	for in, want := range cases {
		if got := diagram.Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
