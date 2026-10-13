package agent

import (
	"sort"
	"strconv"
	"strings"

	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/governance"
)

// The propose-time gate on a diagram change, for the reasons planfields.go
// gives about the plan.
//
// One difference in kind, and it is why this file exists separately. A plan
// change is a set of fields on a row; a graph change is a statement about a
// *shape* — an arrow needs both its endpoints to be nodes, a box inside a
// boundary needs the boundary to exist and to be the sort of thing that can
// contain one. Those are checkable here, against the graph the tool has already
// read, including against what earlier changes in the same proposal create. Left
// to apply time they fail as `UnapplicableError` after a human has agreed, which
// is the failure this whole layer exists to move earlier.

// diagramChecker validates the changes of one diagram proposal, in order.
type diagramChecker struct {
	graph *dg.Graph

	// Nodes an earlier change in this proposal creates, slug → kind. An edge is
	// allowed to name one, because the edits compose onto the graph in order
	// inside one transaction.
	creating map[string]dg.NodeKind
	// Nodes an earlier change deletes, which an edge may then not name.
	deleting map[string]bool
}

func newDiagramChecker(g *dg.Graph) *diagramChecker {
	return &diagramChecker{
		graph:    g,
		creating: map[string]dg.NodeKind{},
		deleting: map[string]bool{},
	}
}

// check validates one change and records what it does to the shape.
func (d *diagramChecker) check(i int, target pr.ChangeTarget, op pr.Op, after map[string]any) error {
	if err := d.unknownFields(i, target, after); err != nil {
		return err
	}
	switch target {
	case pr.ChangeNode:
		return d.node(i, op, after)
	case pr.ChangeEdge:
		return d.edge(i, op, after)
	case pr.ChangeDiagram:
		return d.diagram(i, after)
	}
	return nil
}

func (d *diagramChecker) unknownFields(i int, target pr.ChangeTarget, after map[string]any) error {
	settable := governance.SettableDiagramFields(target)
	if len(settable) == 0 || len(after) == 0 {
		return nil
	}
	known := make(map[string]bool, len(settable))
	for _, f := range settable {
		known[f] = true
	}
	var unknown []string
	for key := range after {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	hint := ""
	if target == pr.ChangeNode {
		hint = " What a box is made of goes in `summary`, in prose — there is no field for a technology, " +
			"an owner or a team, and inventing one would put it somewhere nothing reads."
	}
	return refuse("change %d sets %s on a %s, which %s not a field of one. The fields you can set are: %s.%s",
		i+1, strings.Join(quoted(unknown), ", "), target, isAre(len(unknown)), strings.Join(settable, ", "), hint)
}

// node checks a box against the graph it would land in.
func (d *diagramChecker) node(i int, op pr.Op, after map[string]any) error {
	slug := textOf(after["slug"])
	if slug == "" {
		slug = dg.Slugify(textOf(after["name"]))
	}
	if slug == "" {
		return refuse("change %d names no node. A node is addressed by its entity slug — send `slug`, "+
			"or a `name` the slug can be derived from.", i+1)
	}

	kind := dg.NodeKind(textOf(after["kind"]))
	if kind != "" {
		if err := d.fail(i, dg.ValidateNodeKind(kind)); err != nil {
			return err
		}
	} else if op == pr.OpCreate && !d.has(slug) {
		// Every node is a placement of a catalog entity and every entity has a
		// kind, so a create without one fails graph validation after approval.
		// Not defaulted: `component` would be a guess that draws itself into the
		// picture and reads afterwards as something somebody decided.
		return refuse("change %d creates the node `%s` without a `kind`, and a node cannot be without one — "+
			"it is what the box is drawn as and what the traversals read. The kinds are: %s.",
			i+1, slug, strings.Join(kindNames(), ", "))
	}

	if op == pr.OpDelete {
		d.deleting[slug] = true
		delete(d.creating, slug)
		return nil
	}
	if err := d.parent(i, slug, after); err != nil {
		return err
	}
	if op == pr.OpCreate {
		d.creating[slug] = kind
		delete(d.deleting, slug)
	}
	return nil
}

// parent checks containment: the box has to exist, and it has to be the sort of
// thing that can hold another one.
func (d *diagramChecker) parent(i int, slug string, after map[string]any) error {
	parent := textOf(after["parent"])
	if parent == "" {
		return nil
	}
	if parent == slug {
		return refuse("change %d puts `%s` inside itself.", i+1, slug)
	}
	kind, known := d.kindOf(parent)
	if !known {
		return refuse("change %d puts `%s` inside `%s`, which is not a node in this diagram and is not being "+
			"created by this proposal. %s", i+1, slug, parent, d.nodeHint())
	}
	if kind != "" && !kind.CanContain() {
		return refuse("change %d puts `%s` inside `%s`, which is a %s. Only a boundary or a system may contain "+
			"other nodes — for anything else the relation you want is an arrow, most likely `contains` "+
			"as an edge or nothing at all.", i+1, slug, parent, kind)
	}
	return nil
}

// edge checks that both endpoints are boxes that will be there.
//
// The apply path says this too, with a good message. Saying it here is the
// difference between the model fixing a slug it mistyped and a person approving
// an arrow that then refuses to land.
func (d *diagramChecker) edge(i int, op pr.Op, after map[string]any) error {
	rel := dg.Relation(textOf(after["relation"]))
	if rel == "" {
		return refuse("change %d is an edge with no `relation`. Every arrow says what kind of arrow it is; "+
			"use `data_flow` when nothing more specific is true rather than leaving it out.", i+1)
	}
	if err := d.fail(i, dg.ValidateRelation(rel)); err != nil {
		return err
	}
	from, to := textOf(after["from"]), textOf(after["to"])
	if from == "" || to == "" {
		return refuse("change %d is an edge missing `from` or `to`. Both are entity slugs.", i+1)
	}
	if op != pr.OpCreate {
		// An update or a delete is checked against the graph by `graphBefore`,
		// which refuses one naming an arrow that is not drawn.
		return nil
	}
	if from == to {
		return refuse("change %d draws `%s` to itself. An arrow relates two different things.", i+1, from)
	}
	for _, end := range []string{from, to} {
		if _, known := d.kindOf(end); !known {
			return refuse("change %d draws an arrow to `%s`, which is not a node in this diagram and is not "+
				"being created by this proposal. %s Propose the node in the same proposal, before the arrow.",
				i+1, end, d.nodeHint())
		}
	}
	return nil
}

func (d *diagramChecker) diagram(i int, after map[string]any) error {
	if level := textOf(after["level"]); level != "" {
		return d.fail(i, dg.ValidateLevel(dg.Level(level)))
	}
	return nil
}

// ── what the graph holds, plus what this proposal is adding to it ────────────

func (d *diagramChecker) has(slug string) bool {
	_, ok := d.kindOf(slug)
	return ok
}

// kindOf answers both "is this box going to be there" and "what is it", against
// the graph as it stands amended by the changes read so far.
func (d *diagramChecker) kindOf(slug string) (dg.NodeKind, bool) {
	if d.deleting[slug] {
		return "", false
	}
	if kind, ok := d.creating[slug]; ok {
		return kind, true
	}
	if n := diagramsvc.FindNode(d.graph, slug); n != nil {
		return n.Kind(), true
	}
	return "", false
}

func (d *diagramChecker) nodeHint() string {
	var slugs []string
	if d.graph != nil {
		for _, n := range d.graph.Nodes {
			if s := n.Slug(); s != "" && !d.deleting[s] {
				slugs = append(slugs, strconv.Quote(s))
			}
		}
	}
	for slug := range d.creating {
		slugs = append(slugs, strconv.Quote(slug))
	}
	sort.Strings(slugs)
	if len(slugs) == 0 {
		return "This diagram has no nodes."
	}
	return "The nodes are: " + strings.Join(slugs, ", ") + "."
}

func (d *diagramChecker) fail(i int, err error) error {
	if err == nil {
		return nil
	}
	return refuse("change %d: %s", i+1, strings.TrimPrefix(err.Error(), "invalid: "))
}

func kindNames() []string {
	kinds := dg.NodeKinds()
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, string(k))
	}
	return out
}
