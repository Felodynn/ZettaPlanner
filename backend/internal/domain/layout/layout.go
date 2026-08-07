// Package layout is the presentation layer of a diagram: coordinates, sizes,
// edge waypoints, collapse state.
//
// It is a separate package from internal/domain/diagram for one reason, and the
// reason is structural (D5). The semantic serializer and the context builder
// must not be able to reach a coordinate — not "must remember to filter them
// out", but must not be able to. Putting presentation in its own package makes
// that an import-graph property, checked by a test that walks the transitive
// closure of internal/serialize looking for anything that touches
// diagram_layout.
//
// If you find yourself wanting to add a field here that a model would benefit
// from reading, it belongs in internal/domain/diagram instead. That is the
// whole test for which side of the line something falls on.
package layout

import (
	"time"

	"github.com/google/uuid"
)

// Placement is one node's presentation state.
type Placement struct {
	DiagramID uuid.UUID
	NodeID    uuid.UUID

	// X, Y, W, H are nil on an `auto` diagram, where nothing is stored and the
	// client computes a dagre layout on render.
	X *float64
	Y *float64
	W *float64
	H *float64

	Collapsed bool

	// Unplaced marks a node that arrived on a `manual` diagram without
	// coordinates — from an accepted agent proposal, or a re-ingest. It is
	// placed near the centroid of its connected neighbours and flagged, and the
	// UI must make it visually distinct. The flag clears on the first human
	// move (§3.2).
	Unplaced bool
}

// Placed reports whether this node has real coordinates.
func (p *Placement) Placed() bool { return p.X != nil && p.Y != nil }

// EdgeRoute is one edge's presentation state.
type EdgeRoute struct {
	DiagramID uuid.UUID
	EdgeID    uuid.UUID
	Waypoints []Point
}

// Point is a waypoint on an edge.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Centroid returns the average position of the placed points, and false when
// none of them are placed.
//
// Used to position an unplaced node near its connected neighbours, which is the
// difference between a new node appearing where the user is looking and a new
// node appearing at the origin, a screen away from everything it connects to.
func Centroid(points []Placement) (Point, bool) {
	var (
		sum Point
		n   int
	)
	for _, p := range points {
		if !p.Placed() {
			continue
		}
		sum.X += *p.X
		sum.Y += *p.Y
		n++
	}
	if n == 0 {
		return Point{}, false
	}
	return Point{X: sum.X / float64(n), Y: sum.Y / float64(n)}, true
}

// Touched is a marker for when a layout row was last written. Not persisted:
// layout writes produce no revision and no audit entry, because moving a box is
// not a semantic change and treating it as one floods the revision log and the
// git projection with noise.
type Touched struct {
	At time.Time
}
