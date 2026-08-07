package diagram

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/layout"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
)

// Layout writes take no lease and produce no revision.
//
// Moving a box is not a semantic change. Treating it as one floods the revision
// log and the git projection with noise, and turns "what changed in this
// diagram last week" into a list of drags. The one thing here that *is* recorded
// is the layout_mode flip, because that is a real and permanent state change.
//
// Any project member may move a box (D32). The ownership table covers the
// semantic layer — "Diagram (semantic) | all | manager" — and a coordinate is
// not an owned field. Requiring a manager to tidy a canvas would mean the people
// reading the diagram most cannot make it readable.

// LayoutNodeInput is one node's requested position.
type LayoutNodeInput struct {
	NodeID    uuid.UUID
	X         *float64
	Y         *float64
	W         *float64
	H         *float64
	Collapsed bool
}

// LayoutEdgeInput is one edge's requested waypoints.
type LayoutEdgeInput struct {
	EdgeID    uuid.UUID
	Waypoints []layout.Point
}

// LayoutInput is a presentation write.
type LayoutInput struct {
	Nodes []LayoutNodeInput
	Edges []LayoutEdgeInput
}

// LayoutResult reports what the write did.
type LayoutResult struct {
	// FlippedToManual is true when this write was the one that made the diagram
	// manual. The UI should say so: it is the moment automatic layout stops
	// happening, and re-enabling it later is destructive.
	FlippedToManual bool
	Mode            string
}

// SaveLayout writes presentation state.
//
// No lease is taken and no revision is produced. The first write carrying a
// coordinate flips the diagram to `manual`, permanently (§3.2).
func (s *Service) SaveLayout(ctx context.Context, diagramID uuid.UUID, in LayoutInput, p policy.Principal) (*LayoutResult, error) {
	result := &LayoutResult{}

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		d, err := s.diagrams.ByID(ctx, tx, diagramID)
		if err != nil {
			return err
		}
		// Membership is the whole check. Deliberately no CheckWrite: there is
		// no ownership rule for a coordinate, and inventing one here would be
		// exactly the authority decision living outside the table that the
		// table exists to prevent.
		if _, err := s.requireMember(ctx, tx, p, d.ProjectID); err != nil {
			return err
		}

		carriesCoordinates := false
		for _, n := range in.Nodes {
			if n.X != nil || n.Y != nil {
				carriesCoordinates = true
				break
			}
		}

		if carriesCoordinates && d.LayoutMode != dg.LayoutManual {
			flipped, err := s.diagrams.SetLayoutMode(ctx, tx, diagramID, dg.LayoutManual)
			if err != nil {
				return err
			}
			if flipped {
				result.FlippedToManual = true
				// The one audited thing on this path. A diagram becoming manual
				// is a decision with a consequence — automatic layout stops,
				// and turning it back on destroys the arrangement.
				if err := audit.Write(ctx, tx, audit.Entry{
					ProjectID: &d.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
					Action: audit.ActionLayoutModeSet, SubjectID: &diagramID,
					Before: map[string]any{"layout_mode": d.LayoutMode},
					After:  map[string]any{"layout_mode": dg.LayoutManual},
				}); err != nil {
					return err
				}
			}
			d.LayoutMode = dg.LayoutManual
		}

		for _, n := range in.Nodes {
			if err := s.layouts.Put(ctx, tx, &layout.Placement{
				DiagramID: diagramID, NodeID: n.NodeID,
				X: n.X, Y: n.Y, W: n.W, H: n.H,
				Collapsed: n.Collapsed,
				// A human placed it, so it is no longer unplaced. The flag
				// clears on the first move (§3.2).
				Unplaced: false,
			}); err != nil {
				return err
			}
		}
		for _, e := range in.Edges {
			if err := s.layouts.PutEdgeRoute(ctx, tx, &layout.EdgeRoute{
				DiagramID: diagramID, EdgeID: e.EdgeID, Waypoints: e.Waypoints,
			}); err != nil {
				return err
			}
		}

		result.Mode = d.LayoutMode
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ResetLayout discards every stored coordinate and returns the diagram to
// automatic layout.
//
// Destructive and irreversible: the hand-arranged positions are not recoverable
// from anything else, which is why §3.2 requires an explicit confirmation rather
// than treating this as a toggle. The confirm is a parameter here so a caller
// cannot reach it by accident.
func (s *Service) ResetLayout(ctx context.Context, diagramID uuid.UUID, confirm bool, p policy.Principal) error {
	if !confirm {
		return fmt.Errorf(
			"%w: re-running automatic layout discards every hand-set position on this diagram and cannot be undone. "+
				"Send confirm=true if that is what you want",
			domain.ErrInvalid)
	}

	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		d, err := s.diagrams.ByID(ctx, tx, diagramID)
		if err != nil {
			return err
		}
		subject, err := s.requireMember(ctx, tx, p, d.ProjectID)
		if err != nil {
			return err
		}
		// Throwing away everybody's arrangement is not the same act as moving
		// one box, so this one does need the diagram's own authority.
		if den := policy.CheckWrite(policy.KindDiagram, []string{"layout_mode"}, subject); den != nil {
			return den
		}

		if err := s.layouts.ResetToAuto(ctx, tx, diagramID); err != nil {
			return err
		}
		if _, err := s.diagrams.SetLayoutMode(ctx, tx, diagramID, dg.LayoutAuto); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &d.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionLayoutModeSet, SubjectID: &diagramID,
			Before: map[string]any{"layout_mode": d.LayoutMode},
			After:  map[string]any{"layout_mode": dg.LayoutAuto, "coordinates_discarded": true},
		})
	})
}
