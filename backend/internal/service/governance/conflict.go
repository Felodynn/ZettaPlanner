package governance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Conflicts reports every recorded premise of a proposal that no longer holds.
//
// Run twice, for two different reasons. On the read path it tells a reviewer
// that a proposal is stale **before** they spend attention deciding about it —
// §7.1 asks GET /proposals/{id} for "changes + current conflicts", and being
// shown a diff that cannot land is worse than being shown nothing. On the
// approve path it runs again inside the applying transaction, because a check
// made outside the transaction that writes is a check something can move
// underneath.
func (s *Service) Conflicts(ctx context.Context, q store.Querier, p *pr.Proposal) ([]pr.Conflict, error) {
	// Graphs are loaded once per diagram rather than per change: a proposal
	// adding four boxes to one picture would otherwise read it four times.
	graphs := map[uuid.UUID]*dg.Graph{}

	var out []pr.Conflict
	for _, c := range p.Changes {
		found, err := s.conflictsFor(ctx, q, p, c, graphs)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

func (s *Service) conflictsFor(
	ctx context.Context, q store.Querier, p *pr.Proposal, c pr.Change, graphs map[uuid.UUID]*dg.Graph,
) ([]pr.Conflict, error) {

	// A create has no premise about an existing row, so there is nothing to be
	// stale against. A create whose element has appeared in the meantime is not
	// a conflict either — the proposal has got what it asked for, and the apply
	// path folds it into an update rather than failing over it.
	if c.Op == pr.OpCreate && c.Target != pr.ChangeSubTask {
		return nil, nil
	}
	if c.TargetID == nil {
		return nil, nil
	}

	switch c.Target {
	case pr.ChangeMilestone:
		m, err := s.milestones.ByID(ctx, q, *c.TargetID)
		if err != nil {
			return gone(c, "milestone", err)
		}
		return pr.CompareSnapshot(c.Seq, c.Target, m.Name, c.Before, c.After, milestoneSnapshot(m)), nil

	case pr.ChangeWorkItem:
		w, err := s.items.ByIDAny(ctx, q, *c.TargetID)
		if err != nil {
			return gone(c, "work item", err)
		}
		if w.DeletedAt != nil {
			return []pr.Conflict{{Seq: c.Seq, Target: c.Target, Element: w.Ref,
				Reason: "has been deleted since this was raised"}}, nil
		}
		return pr.CompareSnapshot(c.Seq, c.Target, w.Ref, c.Before, c.After, itemSnapshot(w)), nil

	case pr.ChangeSubTask:
		// The premise of "add a subtask to ZP-14" is that ZP-14 is still there
		// and is still the item somebody meant. Its status is not part of that:
		// a developer moving the item to `in_progress` has not invalidated a
		// proposal to add work to it.
		w, err := s.items.ByIDAny(ctx, q, *c.TargetID)
		if err != nil {
			return gone(c, "work item", err)
		}
		if w.DeletedAt != nil {
			return []pr.Conflict{{Seq: c.Seq, Target: c.Target, Element: w.Ref,
				Reason: "has been deleted since this was raised, so there is nothing to add a subtask to"}}, nil
		}
		return nil, nil

	case pr.ChangeDiagram:
		d, err := s.diagramRep.ByID(ctx, q, *c.TargetID)
		if err != nil {
			return gone(c, "diagram", err)
		}
		return pr.CompareSnapshot(c.Seq, c.Target, d.Slug, c.Before, c.After, map[string]any{
			"slug": d.Slug, "name": d.Name, "level": string(d.Level),
		}), nil

	case pr.ChangeNode, pr.ChangeEdge:
		g, err := s.graphFor(ctx, q, *c.TargetID, graphs)
		if err != nil {
			return gone(c, "diagram", err)
		}
		return graphConflicts(c, g), nil
	}
	return nil, nil
}

func (s *Service) graphFor(ctx context.Context, q store.Querier, diagramID uuid.UUID, cache map[uuid.UUID]*dg.Graph) (*dg.Graph, error) {
	if g, ok := cache[diagramID]; ok {
		return g, nil
	}
	if _, err := s.diagramRep.ByID(ctx, q, diagramID); err != nil {
		return nil, err
	}
	g, err := s.diagramRep.LoadGraph(ctx, q, diagramID)
	if err != nil {
		return nil, err
	}
	cache[diagramID] = g
	return g, nil
}

// graphConflicts compares one node or edge change against the diagram as it
// stands.
func graphConflicts(c pr.Change, g *dg.Graph) []pr.Conflict {
	switch c.Target {
	case pr.ChangeNode:
		slug := changeString(c, "slug")
		if slug == "" {
			return nil
		}
		n := diagramsvc.FindNode(g, slug)
		if n == nil {
			if c.Op == pr.OpDelete {
				// Already gone. The proposal wanted it removed and it is
				// removed; refusing the whole set over that would be pedantry.
				return nil
			}
			return []pr.Conflict{{Seq: c.Seq, Target: c.Target, Element: slug,
				Reason: "is no longer in this diagram, so there is nothing to update"}}
		}
		return pr.CompareSnapshot(c.Seq, c.Target, slug, c.Before, c.After, diagramsvc.NodeSnapshot(n))

	case pr.ChangeEdge:
		from, to := changeString(c, "from"), changeString(c, "to")
		rel := dg.Relation(changeString(c, "relation"))
		if from == "" || to == "" || rel == "" {
			return nil
		}
		element := fmt.Sprintf("%s %s %s", from, rel, to)
		e := diagramsvc.FindEdge(g, from, to, rel)
		if e == nil {
			if c.Op == pr.OpDelete {
				return nil
			}
			return []pr.Conflict{{Seq: c.Seq, Target: c.Target, Element: element,
				Reason: "is no longer in this diagram, so there is nothing to update"}}
		}
		return pr.CompareSnapshot(c.Seq, c.Target, element, c.Before, c.After, diagramsvc.EdgeSnapshot(g, e))
	}
	return nil
}

// changeString reads an addressing field from `before` if it is recorded there
// and from `after` otherwise.
//
// A node's slug is its identity rather than a value being changed, so it is
// present in both — and a proposal raised before Phase 7 started recording
// `before` still addresses its element correctly.
func changeString(c pr.Change, key string) string {
	for _, m := range []map[string]any{c.Before, c.After} {
		if v, ok := m[key]; ok && v != nil {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
				return s
			}
		}
	}
	return ""
}

// gone turns "the row is not there any more" into a conflict rather than an
// error. A proposal about something that has been deleted is stale, which is a
// thing a reviewer can act on; a 404 from an approve call is not.
func gone(c pr.Change, kind string, err error) ([]pr.Conflict, error) {
	if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	element := kind
	if c.TargetID != nil {
		element = fmt.Sprintf("the %s this change names", kind)
	}
	return []pr.Conflict{{
		Seq: c.Seq, Target: c.Target, Element: element,
		Reason: "no longer exists",
	}}, nil
}

// ── snapshots ────────────────────────────────────────────────────────────────
//
// These mirror what the propose tools record in `before` (agent/session.go).
// They are here rather than shared with the plan service on purpose: the plan's
// `publicWorkItem` is an *audit* snapshot, and letting one function serve both
// would mean a change made for the audit log silently changing which proposals
// are considered stale.

func milestoneSnapshot(m *pl.Milestone) map[string]any {
	return map[string]any{
		"name": m.Name, "intent": m.Intent, "status": string(m.Status), "seq": m.Seq,
		"start_date": pl.FormatDate(m.StartDate), "due_date": pl.FormatDate(m.DueDate),
	}
}

func itemSnapshot(w *pl.WorkItem) map[string]any {
	out := map[string]any{
		"ref": w.Ref, "title": w.Title, "intent": w.Intent,
		"acceptance_criteria": w.Acceptance,
		"status":              string(w.Status), "priority": string(w.Priority),
		"progress_pct": w.ProgressPct,
		"start_date":   pl.FormatDate(w.StartDate), "due_date": pl.FormatDate(w.DueDate),
		"assignee_id": "", "milestone_id": "",
	}
	if w.AssigneeID != nil {
		out["assignee_id"] = w.AssigneeID.String()
	}
	if w.MilestoneID != nil {
		out["milestone_id"] = w.MilestoneID.String()
	}
	return out
}
