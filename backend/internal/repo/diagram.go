package repo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Diagrams is the diagrams aggregate: the diagram row, its semantic nodes and
// edges, and its revisions.
//
// Presentation lives in layout.go, in this same package but reachable only
// through a different type. The structural separation that matters is one level
// up: internal/serialize takes a diagram.Graph, which has no coordinate field,
// and cannot import this package at all.
type Diagrams struct{}

const diagramColumns = `id, project_id, name, slug, level, layout_mode, current_revision,
                        created_at, updated_at, deleted_at`

// Create inserts a diagram.
func (Diagrams) Create(ctx context.Context, q store.Querier, d *diagram.Diagram) error {
	const sql = `
		INSERT INTO diagrams (id, project_id, name, slug, level, layout_mode, current_revision, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 0, $7, $7)`
	_, err := q.Exec(ctx, sql, d.ID, d.ProjectID, d.Name, d.Slug, string(d.Level), d.LayoutMode, d.CreatedAt)
	return conflict(err, "a diagram with that slug already exists in this project")
}

// ByID returns one live diagram.
func (Diagrams) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*diagram.Diagram, error) {
	const sql = `SELECT ` + diagramColumns + ` FROM diagrams WHERE id = $1 AND deleted_at IS NULL`
	return scanDiagram(q.QueryRow(ctx, sql, id))
}

// ListByProject returns a project's live diagrams.
// BySlug resolves a diagram by its stable slug within a project.
func (Diagrams) BySlug(ctx context.Context, q store.Querier, projectID uuid.UUID, slug string) (*diagram.Diagram, error) {
	const sql = `SELECT ` + diagramColumns + ` FROM diagrams
	              WHERE project_id = $1 AND slug = $2 AND deleted_at IS NULL`
	return scanDiagram(q.QueryRow(ctx, sql, projectID, slug))
}

func (Diagrams) ListByProject(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]*diagram.Diagram, error) {
	const sql = `SELECT ` + diagramColumns + `
	               FROM diagrams WHERE project_id = $1 AND deleted_at IS NULL
	              ORDER BY level, slug`
	rows, err := q.Query(ctx, sql, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*diagram.Diagram
	for rows.Next() {
		d, err := scanDiagram(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Rename changes the display name. The slug is untouched.
func (Diagrams) Rename(ctx context.Context, q store.Querier, id uuid.UUID, name string) error {
	const sql = `UPDATE diagrams SET name = $2, updated_at = now() WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("diagram")
	}
	return nil
}

// Retitle changes the display name and the level together.
//
// The slug is untouched, here as in Rename: a rename that moved the identity
// would look like a delete plus an add to every reference and to the git
// projection (§6.2).
func (Diagrams) Retitle(ctx context.Context, q store.Querier, id uuid.UUID, name, level string) error {
	const sql = `UPDATE diagrams SET name = $2, level = $3, updated_at = now()
	              WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id, name, level)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("diagram")
	}
	return nil
}

// Delete soft-deletes a diagram.
func (Diagrams) Delete(ctx context.Context, q store.Querier, id uuid.UUID) error {
	const sql = `UPDATE diagrams SET deleted_at = now(), updated_at = now() WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("diagram")
	}
	return nil
}

// SetLayoutMode flips a diagram between auto and manual.
//
// The flip to manual happens on the first coordinate write and is permanent
// without an explicit destructive confirm (§3.2). Returns whether it changed,
// so the caller can audit the transition without re-reading.
func (Diagrams) SetLayoutMode(ctx context.Context, q store.Querier, id uuid.UUID, mode string) (bool, error) {
	const sql = `UPDATE diagrams SET layout_mode = $2, updated_at = now()
	              WHERE id = $1 AND layout_mode <> $2 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id, mode)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// BumpRevision increments current_revision, but only if it still equals from.
//
// The guard is the concurrency control on the save path: two saves that both
// passed the base-revision check earlier cannot both land, because the second
// one's UPDATE matches no row. Returns the new revision.
func (Diagrams) BumpRevision(ctx context.Context, q store.Querier, id uuid.UUID, from int) (int, error) {
	const sql = `UPDATE diagrams SET current_revision = current_revision + 1, updated_at = now()
	              WHERE id = $1 AND current_revision = $2 AND deleted_at IS NULL
	          RETURNING current_revision`
	var next int
	if err := q.QueryRow(ctx, sql, id, from).Scan(&next); err != nil {
		return 0, notFound(err, "diagram at that revision")
	}
	return next, nil
}

func scanDiagram(row scanner) (*diagram.Diagram, error) {
	var (
		d     diagram.Diagram
		level string
	)
	err := row.Scan(&d.ID, &d.ProjectID, &d.Name, &d.Slug, &level, &d.LayoutMode,
		&d.CurrentRevision, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt)
	if err != nil {
		return nil, notFound(err, "diagram")
	}
	d.Level = diagram.Level(level)
	return &d, nil
}

// ── nodes ────────────────────────────────────────────────────────────────────

// InsertNode adds a placement.
func (Diagrams) InsertNode(ctx context.Context, q store.Querier, n *diagram.Node) error {
	attrs, err := json.Marshal(orEmpty(n.Attrs))
	if err != nil {
		return err
	}
	const sql = `
		INSERT INTO diagram_nodes
		  (id, diagram_id, entity_id, label_override, attrs, parent_node_id,
		   provenance, confidence, actor_id, origin_revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err = q.Exec(ctx, sql,
		n.ID, n.DiagramID, n.EntityID, nullIfEmpty(n.LabelOverride), attrs, n.ParentNodeID,
		string(n.Provenance), n.Confidence, n.ActorID, n.OriginRevision)
	return conflict(err, "that entity is already placed in this diagram")
}

// UpdateNode writes a placement's mutable fields.
func (Diagrams) UpdateNode(ctx context.Context, q store.Querier, n *diagram.Node) error {
	attrs, err := json.Marshal(orEmpty(n.Attrs))
	if err != nil {
		return err
	}
	const sql = `
		UPDATE diagram_nodes
		   SET label_override = $2, attrs = $3, parent_node_id = $4, provenance = $5, confidence = $6
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, n.ID, nullIfEmpty(n.LabelOverride), attrs, n.ParentNodeID,
		string(n.Provenance), n.Confidence)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("diagram node")
	}
	return nil
}

// DeleteNode soft-deletes a placement and the edges touching it.
//
// Both in one statement pair rather than leaving the caller to remember: an
// edge whose endpoint is gone is not a relation, it is a dangling pointer that
// the serializer would have to filter and the graph query would have to skip.
func (Diagrams) DeleteNode(ctx context.Context, q store.Querier, nodeID uuid.UUID) error {
	const edges = `UPDATE diagram_edges SET deleted_at = now()
	                WHERE (from_node_id = $1 OR to_node_id = $1) AND deleted_at IS NULL`
	if _, err := q.Exec(ctx, edges, nodeID); err != nil {
		return err
	}
	const node = `UPDATE diagram_nodes SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, node, nodeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("diagram node")
	}
	return nil
}

// ── edges ────────────────────────────────────────────────────────────────────

// InsertEdge adds an edge.
func (Diagrams) InsertEdge(ctx context.Context, q store.Querier, e *diagram.Edge) error {
	attrs, err := json.Marshal(orEmpty(e.Attrs))
	if err != nil {
		return err
	}
	const sql = `
		INSERT INTO diagram_edges
		  (id, diagram_id, from_node_id, to_node_id, relation, label, attrs,
		   provenance, confidence, actor_id, origin_revision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	_, err = q.Exec(ctx, sql,
		e.ID, e.DiagramID, e.FromNodeID, e.ToNodeID, string(e.Relation), nullIfEmpty(e.Label),
		attrs, string(e.Provenance), e.Confidence, e.ActorID, e.OriginRevision)
	return conflict(err, "that edge already exists between those two nodes")
}

// UpdateEdge writes an edge's mutable fields. The identifying triple (from, to,
// relation) is not among them: changing it is a delete plus an add, which is
// what it actually is.
func (Diagrams) UpdateEdge(ctx context.Context, q store.Querier, e *diagram.Edge) error {
	attrs, err := json.Marshal(orEmpty(e.Attrs))
	if err != nil {
		return err
	}
	const sql = `
		UPDATE diagram_edges SET label = $2, attrs = $3, provenance = $4, confidence = $5
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, e.ID, nullIfEmpty(e.Label), attrs, string(e.Provenance), e.Confidence)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("diagram edge")
	}
	return nil
}

// DeleteEdge soft-deletes an edge.
func (Diagrams) DeleteEdge(ctx context.Context, q store.Querier, edgeID uuid.UUID) error {
	const sql = `UPDATE diagram_edges SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, edgeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("diagram edge")
	}
	return nil
}

// ── loading a whole graph ────────────────────────────────────────────────────

// LoadGraph reads a diagram's complete semantic state.
//
// This is what the serializer and the context builder are given. It reads no
// layout table, and the type it returns has nowhere to put a coordinate.
func (r Diagrams) LoadGraph(ctx context.Context, q store.Querier, diagramID uuid.UUID) (*diagram.Graph, error) {
	d, err := r.ByID(ctx, q, diagramID)
	if err != nil {
		return nil, err
	}

	nodes, err := r.loadNodes(ctx, q, diagramID)
	if err != nil {
		return nil, err
	}
	edges, err := r.loadEdges(ctx, q, diagramID)
	if err != nil {
		return nil, err
	}

	// Resolve containment parents to slugs, so a node serialized on its own
	// still names its parent by stable identity.
	slugByNodeID := make(map[uuid.UUID]string, len(nodes))
	for _, n := range nodes {
		slugByNodeID[n.ID] = n.Slug()
	}
	entityIDs := make([]uuid.UUID, 0, len(nodes))
	for _, n := range nodes {
		if n.ParentNodeID != nil {
			n.ParentSlug = slugByNodeID[*n.ParentNodeID]
		}
		entityIDs = append(entityIDs, n.EntityID)
	}

	notes, err := (Notes{}).ForDiagram(ctx, q, d, entityIDs)
	if err != nil {
		return nil, err
	}

	return &diagram.Graph{Diagram: d, Nodes: nodes, Edges: edges, Notes: notes}, nil
}

func (Diagrams) loadNodes(ctx context.Context, q store.Querier, diagramID uuid.UUID) ([]*diagram.Node, error) {
	const sql = `
		SELECT n.id, n.diagram_id, n.entity_id, n.label_override, n.attrs, n.parent_node_id,
		       n.provenance, n.confidence, n.actor_id, n.origin_revision, n.deleted_at,
		       e.id, e.project_id, e.kind, e.name, e.slug, e.summary, e.attrs,
		       e.provenance, e.actor_id, e.created_at, e.updated_at, e.deleted_at
		  FROM diagram_nodes n
		  JOIN catalog_entities e ON e.id = n.entity_id
		 WHERE n.diagram_id = $1 AND n.deleted_at IS NULL
		 ORDER BY e.kind, e.slug`
	rows, err := q.Query(ctx, sql, diagramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*diagram.Node
	for rows.Next() {
		var (
			n              diagram.Node
			e              diagram.Entity
			labelOverride  *string
			nodeAttrs      []byte
			nodeProvenance string
			entKind        string
			entSummary     *string
			entAttrs       []byte
			entProvenance  string
		)
		err := rows.Scan(
			&n.ID, &n.DiagramID, &n.EntityID, &labelOverride, &nodeAttrs, &n.ParentNodeID,
			&nodeProvenance, &n.Confidence, &n.ActorID, &n.OriginRevision, &n.DeletedAt,
			&e.ID, &e.ProjectID, &entKind, &e.Name, &e.Slug, &entSummary, &entAttrs,
			&entProvenance, &e.ActorID, &e.CreatedAt, &e.UpdatedAt, &e.DeletedAt)
		if err != nil {
			return nil, err
		}
		if labelOverride != nil {
			n.LabelOverride = *labelOverride
		}
		n.Provenance = diagram.Provenance(nodeProvenance)
		if err := json.Unmarshal(orEmptyJSON(nodeAttrs), &n.Attrs); err != nil {
			return nil, err
		}
		e.Kind = diagram.NodeKind(entKind)
		e.Provenance = diagram.Provenance(entProvenance)
		if entSummary != nil {
			e.Summary = *entSummary
		}
		if err := json.Unmarshal(orEmptyJSON(entAttrs), &e.Attrs); err != nil {
			return nil, err
		}
		n.Entity = &e
		out = append(out, &n)
	}
	return out, rows.Err()
}

func (Diagrams) loadEdges(ctx context.Context, q store.Querier, diagramID uuid.UUID) ([]*diagram.Edge, error) {
	const sql = `
		SELECT id, diagram_id, from_node_id, to_node_id, relation, label, attrs,
		       provenance, confidence, actor_id, origin_revision, deleted_at
		  FROM diagram_edges
		 WHERE diagram_id = $1 AND deleted_at IS NULL
		 ORDER BY relation, id`
	rows, err := q.Query(ctx, sql, diagramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*diagram.Edge
	for rows.Next() {
		var (
			e          diagram.Edge
			relation   string
			label      *string
			attrs      []byte
			provenance string
		)
		err := rows.Scan(&e.ID, &e.DiagramID, &e.FromNodeID, &e.ToNodeID, &relation, &label,
			&attrs, &provenance, &e.Confidence, &e.ActorID, &e.OriginRevision, &e.DeletedAt)
		if err != nil {
			return nil, err
		}
		e.Relation = diagram.Relation(relation)
		e.Provenance = diagram.Provenance(provenance)
		if label != nil {
			e.Label = *label
		}
		if err := json.Unmarshal(orEmptyJSON(attrs), &e.Attrs); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// ── revisions ────────────────────────────────────────────────────────────────

// Revision is one atomic diagram save.
type Revision struct {
	ID        uuid.UUID
	DiagramID uuid.UUID
	Seq       int
	ActorID   uuid.UUID
	Summary   string
	Changes   RevisionChanges
	CreatedAt time.Time
}

// RevisionChanges is the recorded shape of one save. It is what a stale-base
// rejection hands back, so the caller can see what moved underneath them rather
// than being told only that something did.
type RevisionChanges struct {
	NodesAdded   []string `json:"nodes_added,omitempty"`
	NodesRemoved []string `json:"nodes_removed,omitempty"`
	NodesUpdated []string `json:"nodes_updated,omitempty"`
	EdgesAdded   []string `json:"edges_added,omitempty"`
	EdgesRemoved []string `json:"edges_removed,omitempty"`
	EdgesUpdated []string `json:"edges_updated,omitempty"`
}

// Empty reports whether anything at all changed.
func (c RevisionChanges) Empty() bool {
	return len(c.NodesAdded)+len(c.NodesRemoved)+len(c.NodesUpdated)+
		len(c.EdgesAdded)+len(c.EdgesRemoved)+len(c.EdgesUpdated) == 0
}

// InsertRevision records a save.
func (Diagrams) InsertRevision(ctx context.Context, q store.Querier, rev *Revision) error {
	changes, err := json.Marshal(rev.Changes)
	if err != nil {
		return err
	}
	const sql = `
		INSERT INTO diagram_revisions (id, diagram_id, seq, actor_id, summary, changes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err = q.Exec(ctx, sql, rev.ID, rev.DiagramID, rev.Seq, rev.ActorID,
		nullIfEmpty(rev.Summary), changes, rev.CreatedAt)
	return conflict(err, "that revision already exists")
}

// RevisionsSince returns every revision after seq, oldest first.
//
// This is the diff a stale-base rejection carries: not "something changed" but
// the list of saves that landed, who made them, and what each touched.
func (Diagrams) RevisionsSince(ctx context.Context, q store.Querier, diagramID uuid.UUID, seq int) ([]*Revision, error) {
	const sql = `
		SELECT id, diagram_id, seq, actor_id, summary, changes, created_at
		  FROM diagram_revisions
		 WHERE diagram_id = $1 AND seq > $2
		 ORDER BY seq`
	return scanRevisions(q.Query(ctx, sql, diagramID, seq))
}

// ListRevisions returns a diagram's revisions, newest first.
func (Diagrams) ListRevisions(ctx context.Context, q store.Querier, diagramID uuid.UUID, limit int) ([]*Revision, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const sql = `
		SELECT id, diagram_id, seq, actor_id, summary, changes, created_at
		  FROM diagram_revisions
		 WHERE diagram_id = $1
		 ORDER BY seq DESC
		 LIMIT $2`
	return scanRevisions(q.Query(ctx, sql, diagramID, limit))
}

func scanRevisions(rows rowsIface, err error) ([]*Revision, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Revision
	for rows.Next() {
		var (
			r       Revision
			summary *string
			changes []byte
		)
		if err := rows.Scan(&r.ID, &r.DiagramID, &r.Seq, &r.ActorID, &summary, &changes, &r.CreatedAt); err != nil {
			return nil, err
		}
		if summary != nil {
			r.Summary = *summary
		}
		if len(changes) > 0 {
			if err := json.Unmarshal(changes, &r.Changes); err != nil {
				return nil, err
			}
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func orEmptyJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}
