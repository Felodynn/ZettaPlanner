package diagram

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
)

// Element-level edits, which is what an approved proposal applies.
//
// Neither of the two existing entry points fits. Save is a human at a canvas
// sending complete state, so it needs a lease and a base revision. Apply is
// additive evidence, so it cannot remove anything — and a proposal that says
// "the direct call from the API to the database should go through the cache"
// removes an arrow. This third form is neither: a small set of named edits,
// composed onto the graph as it stands **inside the transaction that reads it**,
// landing as exactly one revision.
//
// It takes no lease and no base revision on purpose. There is nothing to be
// stale against: the graph is read and written in one transaction, and the
// question the base revision answers for a human — has the thing I am editing
// moved since I looked — is answered for a proposal by the conflict check
// against its recorded `before`, which is a sharper question than a revision
// counter. A human holding a lease is not locked out; their next save carries a
// base revision and gets the usual 409 naming what landed.

// EditOp is what one edit does.
type EditOp string

const (
	EditCreate EditOp = "create"
	EditUpdate EditOp = "update"
	EditDelete EditOp = "delete"
)

// DiagramEdit renames or re-levels the diagram itself.
type DiagramEdit struct {
	Name  string
	Level dg.Level
}

// GraphEdit is one element-level change. Exactly one of Node, Edge or Diagram
// is set.
type GraphEdit struct {
	Op EditOp

	Node    *NodeInput
	Edge    *EdgeInput
	Diagram *DiagramEdit
}

// EditsInput is a set of edits landing as one revision.
type EditsInput struct {
	DiagramID uuid.UUID
	Summary   string
	Edits     []GraphEdit

	// Action is the audit action recorded. Named by the caller because
	// `proposal.applied` and `diagram.revised` are different events even though
	// they write the same rows.
	Action audit.Action
	// AuditAfter is merged into the audit entry, so the record says which
	// proposal this came from.
	AuditAfter map[string]any
}

// UnapplicableError is an edit that cannot land against the current graph.
//
// Distinct from a conflict: a conflict is "the thing you proposed to change has
// moved", which supersedes a proposal. This is "the thing you proposed to change
// is not there at all", which usually means the proposal was written against a
// diagram it was not read from.
type UnapplicableError struct {
	Element string
	Reason  string
}

func (e *UnapplicableError) Error() string {
	return fmt.Sprintf("%s: %s", e.Element, e.Reason)
}

func (e *UnapplicableError) Is(target error) bool { return target == domain.ErrConflict }

// ApplyEditsInTx composes the edits onto the current graph and lands one
// revision inside the caller's transaction.
func (s *Service) ApplyEditsInTx(ctx context.Context, tx pgx.Tx, in EditsInput, p policy.Principal) (*ApplyResult, error) {
	if in.Action == "" {
		return nil, fmt.Errorf("%w: an edit set must name the audit action it records", domain.ErrInvalid)
	}

	d, err := s.diagrams.ByID(ctx, tx, in.DiagramID)
	if err != nil {
		return nil, err
	}
	subject, err := s.requireMember(ctx, tx, p, d.ProjectID)
	if err != nil {
		return nil, err
	}
	// The same CheckWrite as a human save. An approved proposal carries its own
	// authority through policy.Subject rather than through a second code path.
	if den := policy.CheckWrite(policy.KindDiagram, []string{"nodes", "edges"}, subject); den != nil {
		return nil, den
	}

	current, err := s.diagrams.LoadGraph(ctx, tx, in.DiagramID)
	if err != nil {
		return nil, err
	}

	state, err := compose(current, in.Edits)
	if err != nil {
		return nil, err
	}

	desired := SaveInput{Summary: in.Summary, Nodes: state.nodes, Edges: state.edges}
	if err := validateInputVocabulary(desired); err != nil {
		return nil, err
	}

	if state.rename != nil {
		if err := s.applyDiagramEdit(ctx, tx, d, *state.rename, p); err != nil {
			return nil, err
		}
	}

	entities, raised, changedEntities, err := s.resolveEntities(ctx, tx, d, state.nodes, p)
	if err != nil {
		return nil, err
	}

	nextRevision := d.CurrentRevision + 1
	changes, err := s.applyState(ctx, tx, d, current, desired, entities, changedEntities, p, nextRevision, false)
	if err != nil {
		return nil, err
	}

	result := &ApplyResult{DiagramID: d.ID, Diagram: d, Revision: d.CurrentRevision, Changes: changes, NotesRaised: raised}
	if changes.Empty() && state.rename == nil {
		return result, nil
	}

	seq, err := s.diagrams.BumpRevision(ctx, tx, d.ID, d.CurrentRevision)
	if err != nil {
		return nil, err
	}
	if err := s.diagrams.InsertRevision(ctx, tx, &repo.Revision{
		ID: uuid.Must(uuid.NewV7()), DiagramID: d.ID, Seq: seq,
		ActorID: p.WritingActorID(), Summary: in.Summary, Changes: changes, CreatedAt: time.Now(),
	}); err != nil {
		return nil, err
	}
	result.Revision = seq
	d.CurrentRevision = seq

	after := map[string]any{"revision": seq, "summary": in.Summary, "changes": changes}
	for k, v := range in.AuditAfter {
		after[k] = v
	}
	if err := audit.Write(ctx, tx, audit.Entry{
		ProjectID: &d.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
		Action: in.Action, SubjectID: &d.ID,
		Before: map[string]any{"revision": seq - 1},
		After:  after,
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) applyDiagramEdit(ctx context.Context, tx pgx.Tx, d *dg.Diagram, e DiagramEdit, p policy.Principal) error {
	before := map[string]any{"name": d.Name, "level": string(d.Level)}
	if name := strings.TrimSpace(e.Name); name != "" {
		d.Name = name
	}
	if e.Level != "" {
		if err := dg.ValidateLevel(e.Level); err != nil {
			return err
		}
		d.Level = e.Level
	}
	// The slug never moves with the name. A rename that changed the identity
	// would look like a delete plus an add to every reference and to the git
	// projection (§6.2).
	if err := s.diagrams.Retitle(ctx, tx, d.ID, d.Name, string(d.Level)); err != nil {
		return err
	}
	return audit.Write(ctx, tx, audit.Entry{
		ProjectID: &d.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
		Action: audit.ActionDiagramUpdated, SubjectID: &d.ID,
		Before: before,
		After:  map[string]any{"name": d.Name, "level": string(d.Level)},
	})
}

// ── composition ──────────────────────────────────────────────────────────────

type composed struct {
	nodes  []NodeInput
	edges  []EdgeInput
	rename *DiagramEdit
}

// compose folds the edits onto the current graph and returns the whole desired
// state.
//
// Pure: no database, no service. The rules that decide what a proposal means —
// deleting a node takes its arrows with it, updating one only touches the
// fields it names — are the ones most worth testing without infrastructure.
func compose(current *dg.Graph, edits []GraphEdit) (*composed, error) {
	nodes := map[string]*NodeInput{}
	var order []string
	for _, n := range current.Nodes {
		slug := n.Slug()
		if slug == "" {
			continue
		}
		nodes[slug] = &NodeInput{
			Slug: slug, Kind: n.Kind(), Name: n.Label(), Attrs: n.Attrs,
			Parent: n.ParentSlug, LabelOverride: n.LabelOverride,
			Confidence: n.Confidence, Provenance: n.Provenance,
		}
		if n.Entity != nil {
			nodes[slug].Name = n.Entity.Name
			nodes[slug].Summary = n.Entity.Summary
		}
		order = append(order, slug)
	}

	edges := map[string]*EdgeInput{}
	var edgeOrder []string
	for _, e := range current.Edges {
		from, to := current.NodeByID(e.FromNodeID), current.NodeByID(e.ToNodeID)
		if from == nil || to == nil {
			continue
		}
		key := edgeKey(from.Slug(), to.Slug(), e.Relation)
		edges[key] = &EdgeInput{
			From: from.Slug(), To: to.Slug(), Relation: e.Relation, Label: e.Label,
			Attrs: e.Attrs, Confidence: e.Confidence, Provenance: e.Provenance,
		}
		edgeOrder = append(edgeOrder, key)
	}

	out := &composed{}
	for i, ed := range edits {
		var err error
		switch {
		case ed.Diagram != nil:
			edit := *ed.Diagram
			out.rename = &edit
		case ed.Node != nil:
			order, err = composeNode(nodes, order, edges, ed, i)
		case ed.Edge != nil:
			edgeOrder, err = composeEdge(edges, edgeOrder, nodes, ed, i)
		default:
			err = &UnapplicableError{
				Element: fmt.Sprintf("change %d", i+1),
				Reason:  "names no node, edge or diagram to change",
			}
		}
		if err != nil {
			return nil, err
		}
	}

	for _, slug := range order {
		if n, ok := nodes[slug]; ok {
			out.nodes = append(out.nodes, *n)
		}
	}
	for _, key := range edgeOrder {
		if e, ok := edges[key]; ok {
			out.edges = append(out.edges, *e)
		}
	}
	return out, nil
}

func composeNode(nodes map[string]*NodeInput, order []string, edges map[string]*EdgeInput, ed GraphEdit, i int) ([]string, error) {
	in := *ed.Node
	slug := strings.TrimSpace(in.Slug)
	if slug == "" {
		return order, &UnapplicableError{
			Element: fmt.Sprintf("change %d", i+1),
			Reason:  "a node change is addressed by its entity slug, and this one has none",
		}
	}
	existing, present := nodes[slug]

	switch ed.Op {
	case EditCreate:
		if present {
			// Not an error. A proposal saying "add a Redis cache" against a
			// diagram that has since gained one has already got what it wanted,
			// and failing the whole change set over it would be pedantry.
			// Treated as an update so the summary and kind still land.
			mergeNode(existing, in)
			return order, nil
		}
		if in.Provenance == "" {
			in.Provenance = dg.ProvAgentProposed
		}
		if in.Name == "" {
			in.Name = slug
		}
		nodes[slug] = &in
		return append(order, slug), nil

	case EditUpdate:
		if !present {
			return order, &UnapplicableError{
				Element: "node " + slug,
				Reason:  "is not in this diagram, so there is nothing to update",
			}
		}
		mergeNode(existing, in)
		return order, nil

	case EditDelete:
		if !present {
			return order, nil
		}
		delete(nodes, slug)
		// The arrows go with it. An edge to a node that is not there is not a
		// state anybody proposed, and leaving it would fail graph validation
		// after the write rather than here.
		for key, e := range edges {
			if e.From == slug || e.To == slug {
				delete(edges, key)
			}
		}
		// Anything contained by it loses its parent rather than disappearing:
		// deleting a boundary must not delete what was inside it.
		for _, n := range nodes {
			if n.Parent == slug {
				n.Parent = ""
			}
		}
		return order, nil
	}
	return order, &UnapplicableError{
		Element: fmt.Sprintf("change %d", i+1),
		Reason:  fmt.Sprintf("has op %q; the ops are create, update, delete", ed.Op),
	}
}

// mergeNode applies only the fields the edit names. A proposal that changes a
// kind must not blank a summary it never mentioned.
func mergeNode(into *NodeInput, from NodeInput) {
	if from.Kind != "" {
		into.Kind = from.Kind
	}
	if from.Name != "" {
		into.Name = from.Name
	}
	if from.Summary != "" {
		into.Summary = from.Summary
	}
	if from.Parent != "" {
		into.Parent = from.Parent
	}
	if from.LabelOverride != "" {
		into.LabelOverride = from.LabelOverride
	}
	if len(from.Attrs) > 0 {
		into.Attrs = from.Attrs
	}
	into.Provenance = dg.ProvHumanEdited
}

func composeEdge(edges map[string]*EdgeInput, order []string, nodes map[string]*NodeInput, ed GraphEdit, i int) ([]string, error) {
	in := *ed.Edge
	in.From, in.To = strings.TrimSpace(in.From), strings.TrimSpace(in.To)
	if in.From == "" || in.To == "" || in.Relation == "" {
		return order, &UnapplicableError{
			Element: fmt.Sprintf("change %d", i+1),
			Reason:  "an edge is addressed by from, to and relation, and this one is missing at least one",
		}
	}
	key := edgeKey(in.From, in.To, in.Relation)
	existing, present := edges[key]

	switch ed.Op {
	case EditCreate:
		if present {
			if in.Label != "" {
				existing.Label = in.Label
			}
			return order, nil
		}
		for _, slug := range []string{in.From, in.To} {
			if _, ok := nodes[slug]; !ok {
				return order, &UnapplicableError{
					Element: fmt.Sprintf("edge %s %s %s", in.From, in.Relation, in.To),
					Reason: fmt.Sprintf("references %s, which is not a node in this diagram and is not being "+
						"created by this proposal", slug),
				}
			}
		}
		if in.Provenance == "" {
			in.Provenance = dg.ProvAgentProposed
		}
		edges[key] = &in
		return append(order, key), nil

	case EditUpdate:
		if !present {
			return order, &UnapplicableError{
				Element: fmt.Sprintf("edge %s %s %s", in.From, in.Relation, in.To),
				Reason:  "is not in this diagram, so there is nothing to update",
			}
		}
		if in.Label != "" {
			existing.Label = in.Label
		}
		if len(in.Attrs) > 0 {
			existing.Attrs = in.Attrs
		}
		existing.Provenance = dg.ProvHumanEdited
		return order, nil

	case EditDelete:
		delete(edges, key)
		return order, nil
	}
	return order, &UnapplicableError{
		Element: fmt.Sprintf("change %d", i+1),
		Reason:  fmt.Sprintf("has op %q; the ops are create, update, delete", ed.Op),
	}
}

// NodeSnapshot is one node's current state, as a proposal records it in
// `before` and as re-evaluate shows it to the model.
func NodeSnapshot(n *dg.Node) map[string]any {
	out := map[string]any{
		"slug": n.Slug(), "kind": string(n.Kind()), "name": n.Label(),
		// The effective one: a person who corrected this box's kind changed the
		// entity, not the placement, and the placement's own provenance still
		// says `vlm_extracted`.
		"provenance": string(n.EffectiveProvenance()),
	}
	if n.Entity != nil {
		out["name"] = n.Entity.Name
		out["summary"] = n.Entity.Summary
	}
	if n.ParentSlug != "" {
		out["parent"] = n.ParentSlug
	}
	return out
}

// EdgeSnapshot is one edge's current state.
func EdgeSnapshot(g *dg.Graph, e *dg.Edge) map[string]any {
	from, to := g.NodeByID(e.FromNodeID), g.NodeByID(e.ToNodeID)
	out := map[string]any{
		"relation": string(e.Relation), "label": e.Label, "provenance": string(e.Provenance),
	}
	if from != nil {
		out["from"] = from.Slug()
	}
	if to != nil {
		out["to"] = to.Slug()
	}
	return out
}

// FindNode returns a diagram's node by entity slug.
func FindNode(g *dg.Graph, slug string) *dg.Node {
	for _, n := range g.Nodes {
		if n.Slug() == slug {
			return n
		}
	}
	return nil
}

// FindEdge returns a diagram's edge by from, to and relation.
func FindEdge(g *dg.Graph, from, to string, rel dg.Relation) *dg.Edge {
	want := edgeKey(from, to, rel)
	for _, e := range g.Edges {
		f, t := g.NodeByID(e.FromNodeID), g.NodeByID(e.ToNodeID)
		if f == nil || t == nil {
			continue
		}
		if edgeKey(f.Slug(), t.Slug(), e.Relation) == want {
			return e
		}
	}
	return nil
}

// LoadGraphInTx exposes the semantic graph to the services that compose against
// it — governance applying a proposal, re-evaluate describing what a human
// corrected. Read-only, and inside the caller's transaction so what is described
// is what is written.
func (s *Service) LoadGraphInTx(ctx context.Context, tx pgx.Tx, diagramID uuid.UUID) (*dg.Graph, error) {
	return s.diagrams.LoadGraph(ctx, tx, diagramID)
}

// SortedSlugs is a stable listing helper for messages a human reads.
func SortedSlugs(g *dg.Graph) []string {
	out := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		out = append(out, n.Slug())
	}
	sort.Strings(out)
	return out
}
