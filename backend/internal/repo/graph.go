package repo

import (
	"context"

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
