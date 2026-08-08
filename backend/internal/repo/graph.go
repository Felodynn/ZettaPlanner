package repo

import (
	"context"
	"sort"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Graphs answers questions about the architecture graph across every diagram in
// a project.
//
// This is the part a vector store cannot do (§6.3 layer 2). "What depends on the
// token store" is a traversal over typed edges, not a similarity search over
// text, and answering it by embedding would give a plausible list rather than
// the right one.
//
// Traversal is over catalog entities rather than over diagram nodes, because an
// entity is the identity: the auth service drawn in a context diagram and again
// in a component diagram is one thing, and its dependents are the union.
type Graphs struct{}

// Related is one entity reached by traversal, with how it was reached.
type Related struct {
	EntityID uuid.UUID
	Kind     diagram.NodeKind
	Name     string
	Slug     string
	Summary  string

	// Depth is how many hops from the starting entity. 1 is a direct relation.
	Depth int

	// Via names the relation on the first hop and the diagram it was drawn in,
	// so a reader can go and look at the picture the claim came from.
	Relation    diagram.Relation
	DiagramSlug string
	Label       string
}

// DefaultDepth is how far the traversal goes when the caller does not say.
// One hop is "what touches this"; two is usually where a human stops reading.
const DefaultDepth = 2

// MaxDepth bounds the recursion. A depth-limited CTE cannot be talked into
// walking a cycle forever, which matters because the architecture graph is
// allowed to contain them — A calls B and B reads from A is a normal drawing.
const MaxDepth = 6

// Dependents returns the entities that depend on this one, across every diagram
// in the project: everything with a dependency edge pointing *at* it.
func (Graphs) Dependents(ctx context.Context, q store.Querier, projectID, entityID uuid.UUID, depth int) ([]Related, error) {
	return traverse(ctx, q, projectID, entityID, depth, true)
}

// DependsOn returns the entities this one depends on, across every diagram:
// everything its dependency edges point at.
func (Graphs) DependsOn(ctx context.Context, q store.Querier, projectID, entityID uuid.UUID, depth int) ([]Related, error) {
	return traverse(ctx, q, projectID, entityID, depth, false)
}

// traverse walks dependency edges in one direction.
//
// `inbound` selects which way: true finds the things that depend on the start
// (edges pointing at it), false finds what the start depends on.
//
// Structural relations are excluded. `contains` and `deploys_to` say where
// something sits, not what it needs, and letting them into this answer turns
// "what depends on the token store" into "everything in the same trust
// boundary" — technically a traversal, useless as an answer.
func traverse(ctx context.Context, q store.Querier, projectID, entityID uuid.UUID, depth int, inbound bool) ([]Related, error) {
	if depth <= 0 {
		depth = DefaultDepth
	}
	if depth > MaxDepth {
		depth = MaxDepth
	}

	// The two directions differ only in which end of the edge is joined to the
	// frontier, so one query serves both rather than two that can drift apart.
	nearSide, farSide := "to_node_id", "from_node_id"
	if !inbound {
		nearSide, farSide = "from_node_id", "to_node_id"
	}

	sql := `
WITH RECURSIVE
-- Every edge in the project, lifted from node ids to entity ids so the walk is
-- over identities rather than over placements.
edge AS (
  SELECT nf.entity_id AS near_entity,
         nt.entity_id AS far_entity,
         e.relation,
         coalesce(e.label, '') AS label,
         d.slug AS diagram_slug
    FROM diagram_edges e
    JOIN diagrams d       ON d.id  = e.diagram_id  AND d.deleted_at IS NULL AND d.project_id = $1
    JOIN diagram_nodes nf ON nf.id = e.` + nearSide + ` AND nf.deleted_at IS NULL
    JOIN diagram_nodes nt ON nt.id = e.` + farSide + `  AND nt.deleted_at IS NULL
   WHERE e.deleted_at IS NULL
     AND e.relation = ANY($4)
),
walk AS (
  SELECT far_entity AS entity_id, 1 AS depth, relation, diagram_slug, label,
         ARRAY[near_entity, far_entity] AS seen
    FROM edge
   WHERE near_entity = $2

  UNION ALL

  SELECT e.far_entity, w.depth + 1, w.relation, w.diagram_slug, w.label,
         w.seen || e.far_entity
    FROM walk w
    JOIN edge e ON e.near_entity = w.entity_id
   WHERE w.depth < $3
     -- The cycle guard. The architecture graph is allowed to contain cycles;
     -- the traversal is not allowed to follow one.
     AND NOT (e.far_entity = ANY(w.seen))
)
SELECT DISTINCT ON (w.entity_id)
       c.id, c.kind, c.name, c.slug, coalesce(c.summary, ''),
       w.depth, w.relation, w.diagram_slug, w.label
  FROM walk w
  JOIN catalog_entities c ON c.id = w.entity_id AND c.deleted_at IS NULL
 WHERE w.entity_id <> $2
 -- Shallowest route wins: if something is both a direct and an indirect
 -- dependent, the direct fact is the one worth reporting.
 ORDER BY w.entity_id, w.depth, w.relation`

	rows, err := q.Query(ctx, sql, projectID, entityID, depth, diagram.DependencyRelations())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Related
	for rows.Next() {
		var (
			r        Related
			kind     string
			relation string
		)
		if err := rows.Scan(&r.EntityID, &kind, &r.Name, &r.Slug, &r.Summary,
			&r.Depth, &relation, &r.DiagramSlug, &r.Label); err != nil {
			return nil, err
		}
		r.Kind = diagram.NodeKind(kind)
		r.Relation = diagram.Relation(relation)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// DISTINCT ON forces an order by entity id; present the result the way a
	// reader wants it instead.
	sortRelated(out)
	return out, nil
}

// Hop is one edge touching an entity, with the far end named and the diagram it
// was drawn in.
//
// There is no coordinate on this type and there must never be one. The
// architecture slice is what a planner reads to answer "what does this touch",
// and geometry in that answer invites it to derive flow from position rather
// than from the relations (D5).
type Hop struct {
	EntityID uuid.UUID
	// Inbound reports whether the edge points at this entity rather than away
	// from it.
	Inbound     bool
	Relation    diagram.Relation
	Label       string
	OtherID     uuid.UUID
	OtherSlug   string
	OtherName   string
	OtherKind   diagram.NodeKind
	DiagramSlug string
}

// Hops returns one hop in each direction around each of the given entities.
//
// This is §6.3's "the diagram slice around those entities: nodes plus one hop",
// and it is deliberately **one** hop. Two hops around five entities is most of
// a project's architecture, arriving in a context window as though every part
// of it were relevant to the question.
//
// Every relation is included, structural ones as well: unlike "what depends on
// X", the question here is "what is drawn around X", and a boundary containing
// something is part of the answer.
func (Graphs) Hops(ctx context.Context, q store.Querier, projectID uuid.UUID, entityIDs []uuid.UUID) ([]Hop, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	const sql = `
		WITH edge AS (
		  SELECT nf.entity_id AS from_entity,
		         nt.entity_id AS to_entity,
		         e.relation,
		         coalesce(e.label, '') AS label,
		         d.slug AS diagram_slug
		    FROM diagram_edges e
		    JOIN diagrams d       ON d.id  = e.diagram_id   AND d.deleted_at IS NULL AND d.project_id = $1
		    JOIN diagram_nodes nf ON nf.id = e.from_node_id AND nf.deleted_at IS NULL
		    JOIN diagram_nodes nt ON nt.id = e.to_node_id   AND nt.deleted_at IS NULL
		   WHERE e.deleted_at IS NULL
		)
		SELECT from_entity, false, relation, label, to_entity, diagram_slug
		  FROM edge WHERE from_entity = ANY($2)
		UNION ALL
		SELECT to_entity, true, relation, label, from_entity, diagram_slug
		  FROM edge WHERE to_entity = ANY($2)`

	rows, err := q.Query(ctx, sql, projectID, entityIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		hops  []Hop
		other = map[uuid.UUID]bool{}
	)
	for rows.Next() {
		var (
			h        Hop
			relation string
		)
		if err := rows.Scan(&h.EntityID, &h.Inbound, &relation, &h.Label, &h.OtherID, &h.DiagramSlug); err != nil {
			return nil, err
		}
		h.Relation = diagram.Relation(relation)
		hops = append(hops, h)
		other[h.OtherID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(hops) == 0 {
		return nil, nil
	}

	// Name the far ends in one round trip. A slice full of UUIDs is a slice a
	// model cannot say anything about.
	ids := make([]uuid.UUID, 0, len(other))
	for id := range other {
		ids = append(ids, id)
	}
	named, err := (Entities{}).ByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	kept := hops[:0]
	for _, h := range hops {
		e, ok := named[h.OtherID]
		if !ok {
			continue // the far end has been deleted; the edge is not worth reporting
		}
		h.OtherSlug, h.OtherName, h.OtherKind = e.Slug, e.Name, e.Kind
		kept = append(kept, h)
	}
	sortHops(kept)
	return kept, nil
}

func sortHops(in []Hop) {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i], in[j]
		if a.EntityID != b.EntityID {
			return a.EntityID.String() < b.EntityID.String()
		}
		if a.Inbound != b.Inbound {
			return !a.Inbound // outgoing first: what this thing does, then what is done to it
		}
		if a.Relation != b.Relation {
			return a.Relation < b.Relation
		}
		return a.OtherSlug < b.OtherSlug
	})
}

func sortRelated(in []Related) {
	// Insertion sort: these lists are small, and pulling in sort for a
	// three-key comparison earns nothing.
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && lessRelated(in[j], in[j-1]); j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

func lessRelated(a, b Related) bool {
	if a.Depth != b.Depth {
		return a.Depth < b.Depth
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Slug < b.Slug
}
