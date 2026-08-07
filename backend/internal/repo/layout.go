package repo

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/layout"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Layouts is the presentation store: diagram_layout and diagram_edge_layout.
//
// **Nothing that builds model context may reach this type.** The guarantee is
// structural (D5): internal/serialize takes a diagram.Graph, which has no
// coordinate field, and internal/serialize/boundary_test.go walks that package's
// transitive import closure and fails if any package in it so much as mentions
// diagram_layout. This file is the reason that test has something to find.
//
// Writes here produce no revision and take no lease. Moving a box is not a
// semantic change, and treating it as one floods the revision log and the git
// projection with noise.
type Layouts struct{}

// Get returns every stored placement for a diagram, keyed by node id.
func (Layouts) Get(ctx context.Context, q store.Querier, diagramID uuid.UUID) (map[uuid.UUID]*layout.Placement, error) {
	const sql = `
		SELECT node_id, x, y, w, h, collapsed, unplaced
		  FROM diagram_layout WHERE diagram_id = $1`
	rows, err := q.Query(ctx, sql, diagramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID]*layout.Placement{}
	for rows.Next() {
		p := &layout.Placement{DiagramID: diagramID}
		if err := rows.Scan(&p.NodeID, &p.X, &p.Y, &p.W, &p.H, &p.Collapsed, &p.Unplaced); err != nil {
			return nil, err
		}
		out[p.NodeID] = p
	}
	return out, rows.Err()
}

// Put writes one placement.
//
// Clearing `unplaced` on write is the "flag clears on first human move" rule
// (§3.2) — expressed here rather than in a caller, because every path that sets
// a coordinate is a human move.
func (Layouts) Put(ctx context.Context, q store.Querier, p *layout.Placement) error {
	const sql = `
		INSERT INTO diagram_layout (diagram_id, node_id, x, y, w, h, collapsed, unplaced)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (diagram_id, node_id) DO UPDATE
		   SET x = EXCLUDED.x, y = EXCLUDED.y, w = EXCLUDED.w, h = EXCLUDED.h,
		       collapsed = EXCLUDED.collapsed, unplaced = EXCLUDED.unplaced`
	_, err := q.Exec(ctx, sql, p.DiagramID, p.NodeID, p.X, p.Y, p.W, p.H, p.Collapsed, p.Unplaced)
	return err
}

// PutUnplaced records a node that arrived without coordinates on a manual
// diagram, positioned near its neighbours and flagged for the UI.
func (Layouts) PutUnplaced(ctx context.Context, q store.Querier, diagramID, nodeID uuid.UUID, at layout.Point) error {
	x, y := at.X, at.Y
	return Layouts{}.Put(ctx, q, &layout.Placement{
		DiagramID: diagramID, NodeID: nodeID, X: &x, Y: &y, Unplaced: true,
	})
}

// NeighbourPlacements returns the stored placements of a node's connected
// neighbours, for positioning an unplaced arrival near where the user is
// already looking rather than at the origin.
func (Layouts) NeighbourPlacements(ctx context.Context, q store.Querier, diagramID, nodeID uuid.UUID) ([]layout.Placement, error) {
	const sql = `
		SELECT l.node_id, l.x, l.y, l.w, l.h, l.collapsed, l.unplaced
		  FROM diagram_layout l
		 WHERE l.diagram_id = $1
		   AND l.node_id IN (
		         SELECT CASE WHEN e.from_node_id = $2 THEN e.to_node_id ELSE e.from_node_id END
		           FROM diagram_edges e
		          WHERE e.diagram_id = $1
		            AND e.deleted_at IS NULL
		            AND ($2 IN (e.from_node_id, e.to_node_id)))`
	rows, err := q.Query(ctx, sql, diagramID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []layout.Placement
	for rows.Next() {
		p := layout.Placement{DiagramID: diagramID}
		if err := rows.Scan(&p.NodeID, &p.X, &p.Y, &p.W, &p.H, &p.Collapsed, &p.Unplaced); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PutEdgeRoute writes an edge's waypoints.
func (Layouts) PutEdgeRoute(ctx context.Context, q store.Querier, r *layout.EdgeRoute) error {
	points, err := json.Marshal(orEmptyPoints(r.Waypoints))
	if err != nil {
		return err
	}
	const sql = `
		INSERT INTO diagram_edge_layout (diagram_id, edge_id, waypoints)
		VALUES ($1, $2, $3)
		ON CONFLICT (diagram_id, edge_id) DO UPDATE SET waypoints = EXCLUDED.waypoints`
	_, err = q.Exec(ctx, sql, r.DiagramID, r.EdgeID, points)
	return err
}

// EdgeRoutes returns every stored edge route for a diagram.
func (Layouts) EdgeRoutes(ctx context.Context, q store.Querier, diagramID uuid.UUID) (map[uuid.UUID][]layout.Point, error) {
	const sql = `SELECT edge_id, waypoints FROM diagram_edge_layout WHERE diagram_id = $1`
	rows, err := q.Query(ctx, sql, diagramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID][]layout.Point{}
	for rows.Next() {
		var (
			id     uuid.UUID
			points []byte
		)
		if err := rows.Scan(&id, &points); err != nil {
			return nil, err
		}
		var decoded []layout.Point
		if len(points) > 0 {
			if err := json.Unmarshal(points, &decoded); err != nil {
				return nil, err
			}
		}
		out[id] = decoded
	}
	return out, rows.Err()
}

// ClearForNode removes a deleted node's presentation rows.
//
// The only removal in this file, and it is not history: a coordinate for a node
// that no longer exists is not a record of anything, and diagram_layout is not
// in CONTRACT §6.5's list of soft-deleted tables. The node itself keeps its
// deleted_at, which is where the history lives.
func (Layouts) ClearForNode(ctx context.Context, q store.Querier, diagramID, nodeID uuid.UUID) error {
	const sql = `DELETE FROM diagram_layout WHERE diagram_id = $1 AND node_id = $2` // zp:hard-delete-ok
	_, err := q.Exec(ctx, sql, diagramID, nodeID)
	return err
}

// ClearForEdge removes a deleted edge's waypoints.
func (Layouts) ClearForEdge(ctx context.Context, q store.Querier, diagramID, edgeID uuid.UUID) error {
	const sql = `DELETE FROM diagram_edge_layout WHERE diagram_id = $1 AND edge_id = $2` // zp:hard-delete-ok
	_, err := q.Exec(ctx, sql, diagramID, edgeID)
	return err
}

// ResetToAuto drops every stored coordinate for a diagram.
//
// Destructive and irreversible, which is why §3.2 requires an explicit confirm
// before re-running auto layout on a manual diagram: the hand-arranged
// positions are not recoverable from anything else.
func (Layouts) ResetToAuto(ctx context.Context, q store.Querier, diagramID uuid.UUID) error {
	const nodes = `DELETE FROM diagram_layout WHERE diagram_id = $1` // zp:hard-delete-ok
	if _, err := q.Exec(ctx, nodes, diagramID); err != nil {
		return err
	}
	const edges = `DELETE FROM diagram_edge_layout WHERE diagram_id = $1` // zp:hard-delete-ok
	_, err := q.Exec(ctx, edges, diagramID)
	return err
}

func orEmptyPoints(p []layout.Point) []layout.Point {
	if p == nil {
		return []layout.Point{}
	}
	return p
}
