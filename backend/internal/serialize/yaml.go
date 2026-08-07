// Package serialize produces the canonical semantic serialization of a diagram.
//
// One serializer, two consumers: the model context (§6.3) and the optional git
// projection (D10). Same bytes.
//
// **This package must never import anything that reads diagram_layout.** That is
// not a convention — internal/serialize/boundary_test.go walks this package's
// transitive import closure and fails on a hit. The only input type is
// diagram.Graph, which has no coordinate field, so "no coordinates in the
// output" holds by construction rather than by filtering (D5).
//
// Determinism is likewise by construction. The output is built as an explicit
// yaml.Node tree with a fixed field order and every collection sorted, rather
// than by marshalling Go maps — map iteration order would make an empty diff
// mean "the marshaller felt different today" instead of "nothing changed".
package serialize

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
)

// Indent is the YAML indent width. Fixed, because it is part of the bytes.
const Indent = 2

// Graph serializes a whole diagram.
func Graph(g *diagram.Graph) ([]byte, error) {
	if g == nil || g.Diagram == nil {
		return nil, fmt.Errorf("serialize: no diagram")
	}

	root := mapping()
	put(root, "diagram", scalar(g.Diagram.Slug))
	put(root, "level", scalar(string(g.Diagram.Level)))

	if nodes := nodeSequence(g); len(nodes.Content) > 0 {
		put(root, "nodes", nodes)
	}
	if edges := edgeSequence(g); len(edges.Content) > 0 {
		put(root, "edges", edges)
	}
	if notes := noteSequence(g); len(notes.Content) > 0 {
		put(root, "notes", notes)
	}

	return encode(root)
}

// Node serializes one node on its own, for the git projection's
// nodes/<entity_slug>.yaml. One file per node is what keeps a diff useful: a
// single big document makes every change touch the same file.
func Node(n *diagram.Node) ([]byte, error) {
	if n == nil || n.Entity == nil {
		return nil, fmt.Errorf("serialize: node has no entity")
	}
	return encode(nodeMapping(n))
}

// Edge serializes one edge on its own, keyed by (from, to, relation).
func Edge(g *diagram.Graph, e *diagram.Edge) ([]byte, error) {
	from, to := g.NodeByID(e.FromNodeID), g.NodeByID(e.ToNodeID)
	if from == nil || to == nil {
		return nil, fmt.Errorf("serialize: edge %s references a node outside the graph", e.ID)
	}
	return encode(edgeMapping(from, to, e))
}

// EdgePath is the stable, slug-derived path for an edge in the git projection.
func EdgePath(from, to *diagram.Node, rel diagram.Relation) string {
	return fmt.Sprintf("edges/%s__%s__%s.yaml", from.Slug(), rel, to.Slug())
}

// NodePath is the stable, slug-derived path for a node. Derived from the entity
// slug and never from a display label: a rename must not look like a delete
// plus an add (§6.2).
func NodePath(n *diagram.Node) string { return "nodes/" + n.Slug() + ".yaml" }

// ── sequences, sorted ────────────────────────────────────────────────────────

// nodeSequence emits nodes ordered by kind, then slug (§6.2).
//
// Deliberately not insertion order and not by id: a UUIDv7 is creation order,
// so ordering by it would make the same graph built in a different sequence
// produce different bytes — which is exactly the property the determinism test
// exists to catch.
func nodeSequence(g *diagram.Graph) *yaml.Node {
	live := make([]*diagram.Node, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.DeletedAt == nil && n.Entity != nil {
			live = append(live, n)
		}
	}
	sort.SliceStable(live, func(i, j int) bool {
		if live[i].Kind() != live[j].Kind() {
			return live[i].Kind() < live[j].Kind()
		}
		return live[i].Slug() < live[j].Slug()
	})

	seq := sequence()
	for _, n := range live {
		seq.Content = append(seq.Content, nodeMapping(n))
	}
	return seq
}

func nodeMapping(n *diagram.Node) *yaml.Node {
	m := mapping()
	put(m, "id", scalar(n.Slug()))
	put(m, "kind", scalar(string(n.Kind())))
	put(m, "name", scalar(n.Label()))

	if summary := strings.TrimSpace(n.Entity.Summary); summary != "" {
		put(m, "summary", prose(summary))
	}
	if n.ParentSlug != "" {
		put(m, "parent", scalar(n.ParentSlug))
	}

	// Provenance travels with the object. It is the input the re-evaluate flow
	// depends on (§5.4): showing the vision model which of its extractions a
	// human corrected is the highest-signal feedback it will ever get.
	put(m, "provenance", scalar(string(n.Provenance)))
	if n.Confidence != nil {
		put(m, "confidence", scalar(formatFloat(float64(*n.Confidence))))
	}
	if attrs := attrsMapping(mergeAttrs(n.Entity.Attrs, n.Attrs)); attrs != nil {
		put(m, "attrs", attrs)
	}
	return m
}

// edgeSequence emits edges ordered by (from, to, relation) — the key that
// identifies an edge.
func edgeSequence(g *diagram.Graph) *yaml.Node {
	type pair struct {
		from, to *diagram.Node
		edge     *diagram.Edge
	}
	live := make([]pair, 0, len(g.Edges))
	for _, e := range g.Edges {
		if e.DeletedAt != nil {
			continue
		}
		from, to := g.NodeByID(e.FromNodeID), g.NodeByID(e.ToNodeID)
		if from == nil || to == nil || from.DeletedAt != nil || to.DeletedAt != nil {
			continue
		}
		live = append(live, pair{from, to, e})
	}
	sort.SliceStable(live, func(i, j int) bool {
		if live[i].from.Slug() != live[j].from.Slug() {
			return live[i].from.Slug() < live[j].from.Slug()
		}
		if live[i].to.Slug() != live[j].to.Slug() {
			return live[i].to.Slug() < live[j].to.Slug()
		}
		return live[i].edge.Relation < live[j].edge.Relation
	})

	seq := sequence()
	for _, p := range live {
		seq.Content = append(seq.Content, edgeMapping(p.from, p.to, p.edge))
	}
	return seq
}

func edgeMapping(from, to *diagram.Node, e *diagram.Edge) *yaml.Node {
	m := mapping()
	put(m, "from", scalar(from.Slug()))
	put(m, "to", scalar(to.Slug()))
	put(m, "relation", scalar(string(e.Relation)))
	if label := strings.TrimSpace(e.Label); label != "" {
		put(m, "label", scalar(label))
	}
	put(m, "provenance", scalar(string(e.Provenance)))
	if e.Confidence != nil {
		put(m, "confidence", scalar(formatFloat(float64(*e.Confidence))))
	}
	if attrs := attrsMapping(e.Attrs); attrs != nil {
		put(m, "attrs", attrs)
	}
	return m
}

// noteSequence emits open notes, ordered by subject then kind then body.
//
// Only open ones: a resolved note is history, and carrying it into every prompt
// spends context re-litigating something already settled. Resolved notes stay
// queryable in Postgres.
func noteSequence(g *diagram.Graph) *yaml.Node {
	live := make([]*diagram.Note, 0, len(g.Notes))
	for _, n := range g.Notes {
		if n.DeletedAt == nil && n.Status == diagram.NoteOpen {
			live = append(live, n)
		}
	}
	sort.SliceStable(live, func(i, j int) bool {
		if live[i].SubjectSlug != live[j].SubjectSlug {
			return live[i].SubjectSlug < live[j].SubjectSlug
		}
		if live[i].Kind != live[j].Kind {
			return live[i].Kind < live[j].Kind
		}
		return live[i].Body < live[j].Body
	})

	seq := sequence()
	for _, n := range live {
		m := mapping()
		put(m, "kind", scalar(string(n.Kind)))
		if n.SubjectSlug != "" {
			put(m, "subject", scalar(n.SubjectSlug))
		}
		put(m, "body", prose(strings.TrimSpace(n.Body)))
		seq.Content = append(seq.Content, m)
	}
	return seq
}

// ── attrs ────────────────────────────────────────────────────────────────────

// mergeAttrs overlays the node's per-placement attrs on the entity's. The
// placement wins, because it is the more specific statement.
func mergeAttrs(entity, node map[string]any) map[string]any {
	if len(entity) == 0 && len(node) == 0 {
		return nil
	}
	out := make(map[string]any, len(entity)+len(node))
	for k, v := range entity {
		out[k] = v
	}
	for k, v := range node {
		out[k] = v
	}
	return out
}

// attrsMapping emits an attrs map with sorted keys, or nil when empty.
func attrsMapping(attrs map[string]any) *yaml.Node {
	if len(attrs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	m := mapping()
	for _, k := range keys {
		put(m, k, value(attrs[k]))
	}
	if len(m.Content) == 0 {
		return nil
	}
	return m
}

// value converts a decoded JSON value into a deterministic YAML node.
func value(v any) *yaml.Node {
	switch t := v.(type) {
	case nil:
		return scalarTagged("null", "!!null")
	case bool:
		return scalarTagged(strconv.FormatBool(t), "!!bool")
	case string:
		if strings.Contains(t, "\n") {
			return prose(t)
		}
		return scalar(t)
	case float64:
		return scalarTagged(formatFloat(t), numberTag(t))
	case float32:
		return scalarTagged(formatFloat(float64(t)), numberTag(float64(t)))
	case int:
		return scalarTagged(strconv.Itoa(t), "!!int")
	case int64:
		return scalarTagged(strconv.FormatInt(t, 10), "!!int")
	case []any:
		seq := sequence()
		for _, item := range t {
			seq.Content = append(seq.Content, value(item))
		}
		return seq
	case map[string]any:
		if m := attrsMapping(t); m != nil {
			return m
		}
		return mapping()
	default:
		return scalar(fmt.Sprintf("%v", t))
	}
}

// formatFloat normalises a number so the same value always prints the same way.
//
// JSONB round-trips every number through float64, so an attribute written as 3
// comes back as 3.0. Printing it as 3.0 makes a diff appear the first time a
// row is re-read, having changed nothing.
func formatFloat(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func numberTag(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return "!!int"
	}
	return "!!float"
}

// ── yaml.Node helpers ────────────────────────────────────────────────────────

func mapping() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func sequence() *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
}

func scalar(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func scalarTagged(s, tag string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: s}
}

// prose emits multi-line or long text as a folded block, so that comments and
// multiline prose diff readably (§6.2) instead of turning into one long line
// that changes entirely when a word is added near the start.
func prose(s string) *yaml.Node {
	if strings.Contains(s, "\n") {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.LiteralStyle}
	}
	if len(s) > 72 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s, Style: yaml.FoldedStyle}
	}
	return scalar(s)
}

// put appends a key/value pair. Order of calls is the order in the output; the
// fixed field order is therefore the order this file reads in.
func put(m *yaml.Node, key string, v *yaml.Node) {
	if v == nil {
		return
	}
	m.Content = append(m.Content, scalar(key), v)
}

func encode(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(Indent)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("serialize: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("serialize: %w", err)
	}
	return buf.Bytes(), nil
}
