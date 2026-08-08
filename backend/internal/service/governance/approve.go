package governance

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Approve decides a proposal and applies it.
//
// One transaction from the authority check to the last write. §5.1 says a
// proposal is applied atomically as one revision or not at all, and the only
// way to mean that is for the decision, every change and the audit trail to
// share a transaction: an approval that committed while its third change failed
// would leave a proposal marked `approved` that changed two things out of three,
// which is the worst state in the system — it looks decided and it is not.
func (s *Service) Approve(ctx context.Context, id uuid.UUID, p policy.Principal) (*pr.Proposal, error) {
	var (
		out *pr.Proposal
		// Carried out of the transaction rather than returned from inside it.
		// Superseding is a **decision** and has to commit: returning the
		// conflict from the closure rolls back the very row that records it,
		// leaving the proposal pending and the reviewer asked again tomorrow.
		// Found by the test, which is the only way this one gets found.
		stale *pr.ConflictError
	)

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		prop, err := s.proposals.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.requireMember(ctx, tx, p, prop.ProjectID); err != nil {
			return err
		}
		if prop.Status.Decided() {
			return fmt.Errorf("%w: this proposal was already %s. A decided proposal is not re-decided; "+
				"raise a new one against the current state if it should happen now", domain.ErrConflict, prop.Status)
		}
		if err := s.mayApprove(ctx, tx, prop, p); err != nil {
			return err
		}

		// Re-checked inside the writing transaction. A check made outside it is
		// a check something can move underneath, and this is precisely the
		// window a stale proposal lands in.
		conflicts, err := s.Conflicts(ctx, tx, prop)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			if err := s.supersede(ctx, tx, prop, conflicts, p); err != nil {
				return err
			}
			stale = &pr.ConflictError{ProposalID: prop.ID.String(), Conflicts: conflicts}
			out, err = s.proposals.ByID(ctx, tx, id)
			return err
		}

		revision, err := s.apply(ctx, tx, prop, p)
		if err != nil {
			return err
		}

		ok, err := s.proposals.Decide(ctx, tx, prop.ID, repoDecision(pr.StatusApproved, p.UserID, "", revision))
		if err != nil {
			return err
		}
		if !ok {
			// Somebody decided it between the read and here. The guarded UPDATE
			// is what makes that a lost race rather than a double application.
			return fmt.Errorf("%w: that proposal was decided by somebody else while this request was running",
				domain.ErrConflict)
		}

		after := map[string]any{"status": string(pr.StatusApproved), "changes": len(prop.Changes)}
		if revision != nil {
			after["applied_revision"] = *revision
		}
		if err := audit.Write(ctx, tx, audit.Entry{
			ProjectID: &prop.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionProposalDecided, SubjectID: &prop.ID,
			Before: map[string]any{"status": string(pr.StatusPending)},
			After:  after,
		}); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{
			ProjectID: &prop.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionProposalApplied, SubjectID: &prop.ID,
			After: map[string]any{"title": prop.Title, "target_kind": string(prop.Target), "changes": len(prop.Changes)},
		}); err != nil {
			return err
		}

		out, err = s.proposals.ByID(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}

	s.publish(out.ProjectID, "proposal.decided", map[string]any{
		"proposal_id": out.ID, "status": string(out.Status), "title": out.Title,
		"decided_by": p.UserID, "target_kind": string(out.Target),
	})
	if stale != nil {
		// The supersession is committed and announced; the caller still learns
		// what moved, which is the thing they need in order to decide whether
		// to raise it again.
		return nil, stale
	}
	s.announceApplied(out)
	return out, nil
}

// mayApprove is §5.1's authority rule, expressed through the ownership table
// rather than beside it.
//
// Managers and admins may approve anything. A developer may approve only a
// proposal whose every change they could have written themselves — which is
// exactly what CheckWrite answers, with their own standing and **no** proposal
// authority attached. Writing the rule any other way would mean two places that
// decide who owns `status`, and they would eventually disagree.
func (s *Service) mayApprove(ctx context.Context, q store.Querier, prop *pr.Proposal, p policy.Principal) error {
	subject, err := s.subject(ctx, q, p, prop.ProjectID)
	if err != nil {
		return err
	}
	if subject.EffectiveRole.AtLeast(policy.RoleManager) {
		return nil
	}

	for _, c := range prop.Changes {
		kind, ok := objectKindFor(c.Target)
		if !ok {
			return developerDenial(prop, c, "the architecture graph is manager-owned in every field (§4.2)")
		}
		// Relationship flags come from the row the change is about, in this
		// transaction — the same rule the direct write path follows.
		sub := subject
		if c.Target == pr.ChangeWorkItem || c.Target == pr.ChangeSubTask {
			if c.TargetID == nil {
				return developerDenial(prop, c, "it creates work rather than reporting on your own")
			}
			item, err := s.items.ByIDAny(ctx, q, *c.TargetID)
			if err != nil {
				return err
			}
			sub.IsAssignee = item.AssigneeID != nil && *item.AssigneeID == p.UserID
		}

		var den *policy.Denial
		if c.Op == pr.OpCreate {
			den = policy.CheckCreate(kind, sub)
		} else if c.Op == pr.OpDelete {
			den = policy.CheckDelete(kind, sub)
		} else {
			den = policy.CheckWrite(kind, changedFields(c), sub)
		}
		if den != nil {
			return developerDenial(prop, c, strings.TrimSuffix(den.Message, "."))
		}
	}
	return nil
}

func developerDenial(prop *pr.Proposal, c pr.Change, because string) *policy.Denial {
	return &policy.Denial{
		Kind:   policy.DenialForbidden,
		Object: policy.KindProject,
		Route:  "ask a manager to approve it",
		Message: fmt.Sprintf(
			"Ask a manager to approve %q. Change %d (%s %s) is outside what you own, because %s. "+
				"A developer may approve a proposal only when every change in it is one they could have made "+
				"directly — a mixed proposal is one decision, and it needs the authority for all of it.",
			prop.Title, c.Seq, c.Op, c.Target, because),
	}
}

// changedFields is the set of §4.2 wire names one change touches, sorted so a
// denial names the same field every time.
func changedFields(c pr.Change) []string {
	out := make([]string, 0, len(c.After))
	for f := range c.After {
		if f == "diagram" {
			// Addressing, not a field: the propose tool stamps it so an
			// approval knows which picture a bare node slug belonged to.
			continue
		}
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func objectKindFor(t pr.ChangeTarget) (policy.ObjectKind, bool) {
	switch t {
	case pr.ChangeMilestone:
		return policy.KindMilestone, true
	case pr.ChangeWorkItem:
		return policy.KindWorkItem, true
	case pr.ChangeSubTask:
		return policy.KindSubTask, true
	case pr.ChangeDiagram, pr.ChangeNode, pr.ChangeEdge:
		return policy.KindDiagram, true
	}
	return "", false
}

// ── superseding ──────────────────────────────────────────────────────────────

// supersede marks a stale proposal and reports every conflict.
//
// It writes the decision rather than only failing, so the proposal stops
// appearing in the review queue: a stale proposal that stays pending is asked
// about again tomorrow, and the answer will not have improved. The rationale
// records what moved, which is what a reviewer needs in order to decide whether
// to re-raise.
func (s *Service) supersede(ctx context.Context, tx pgx.Tx, prop *pr.Proposal, conflicts []pr.Conflict, p policy.Principal) error {
	lines := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		lines = append(lines, c.String())
	}
	rationale := "Superseded: the state this was raised against has moved.\n\n" + strings.Join(lines, "\n")

	ok, err := s.proposals.Decide(ctx, tx, prop.ID, repoDecision(pr.StatusSuperseded, p.UserID, rationale, nil))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: that proposal was decided by somebody else while this request was running",
			domain.ErrConflict)
	}
	details := (&pr.ConflictError{ProposalID: prop.ID.String(), Conflicts: conflicts}).Details()
	return audit.Write(ctx, tx, audit.Entry{
		ProjectID: &prop.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
		Action: audit.ActionProposalSuperseded, SubjectID: &prop.ID,
		Before: map[string]any{"status": string(pr.StatusPending)},
		After:  map[string]any{"status": string(pr.StatusSuperseded), "conflicts": details["conflicts"]},
	})
}

func repoDecision(status pr.Status, by uuid.UUID, rationale string, revision *int) repo.Decision {
	return repo.Decision{Status: status, DecidedBy: by, Rationale: rationale, AppliedRevision: revision}
}

// announceApplied tells the canvas and the plan views that something landed.
//
// Two event types rather than one, because §7.2 gives them different meanings
// to the frontend: `diagram.revised` warns somebody who is editing, while
// `plan.changed` asks a list to refetch.
func (s *Service) announceApplied(prop *pr.Proposal) {
	seen := map[uuid.UUID]bool{}
	for _, c := range prop.Changes {
		if c.Target.IsDiagram() && c.TargetID != nil && !seen[*c.TargetID] {
			seen[*c.TargetID] = true
			payload := map[string]any{"diagram_id": *c.TargetID, "actor": prop.DecidedByName, "proposal_id": prop.ID}
			if prop.AppliedRevision != nil {
				payload["revision"] = *prop.AppliedRevision
			}
			s.publish(prop.ProjectID, "diagram.revised", payload)
			continue
		}
		if !c.Target.IsDiagram() {
			payload := map[string]any{"subject_kind": string(c.Target), "proposal_id": prop.ID}
			if c.TargetID != nil {
				payload["subject_id"] = *c.TargetID
			}
			s.publish(prop.ProjectID, "plan.changed", payload)
		}
	}
}

// ── application ──────────────────────────────────────────────────────────────

// apply runs every change in order and returns the diagram revision it produced.
//
// Ordered by seq, because the changes are an ordered list and not a set: a
// proposal that creates a milestone and then moves three items into it means
// something the same changes in another order do not.
func (s *Service) apply(ctx context.Context, tx pgx.Tx, prop *pr.Proposal, p policy.Principal) (*int, error) {
	writer := plansvc.UnderProposal(p, prop.ID)

	// Diagram changes are grouped per diagram and applied as one edit set, so a
	// proposal touching one picture four times produces one revision rather than
	// four. §5.1 says "atomically as one revision" and means it literally.
	byDiagram := map[uuid.UUID][]diagramsvc.GraphEdit{}
	var diagramOrder []uuid.UUID

	for _, c := range prop.Changes {
		if c.Target.IsDiagram() {
			if c.TargetID == nil {
				return nil, unapplicable(c, "names no diagram")
			}
			if _, seen := byDiagram[*c.TargetID]; !seen {
				diagramOrder = append(diagramOrder, *c.TargetID)
			}
			edit, err := graphEdit(c)
			if err != nil {
				return nil, err
			}
			byDiagram[*c.TargetID] = append(byDiagram[*c.TargetID], edit)
			continue
		}
		if err := s.applyPlanChange(ctx, tx, prop.ProjectID, c, writer); err != nil {
			return nil, err
		}
	}

	var revision *int
	for _, diagramID := range diagramOrder {
		res, err := s.diagrams.ApplyEditsInTx(ctx, tx, diagramsvc.EditsInput{
			DiagramID: diagramID,
			Summary:   prop.Title,
			Edits:     byDiagram[diagramID],
			Action:    audit.ActionDiagramRevised,
			AuditAfter: map[string]any{
				"proposal_id": prop.ID.String(), "rationale": prop.Rationale,
			},
		}, p)
		if err != nil {
			return nil, err
		}
		seq := res.Revision
		revision = &seq
	}
	return revision, nil
}

func unapplicable(c pr.Change, reason string) error {
	return &diagramsvc.UnapplicableError{
		Element: fmt.Sprintf("change %d (%s %s)", c.Seq, c.Op, c.Target),
		Reason:  reason,
	}
}
