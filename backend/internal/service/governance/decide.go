package governance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
)

// MinRationale is how short a refusal may be and still be one.
//
// Not a guess at prose quality — a floor under "no", "nope", "wrong". §5.2 makes
// the rationale first-class because it is what stops the agent re-proposing what
// was already refused, and a two-character rationale carries none of that. Long
// enough to require a reason; short enough that "the queue is deliberate" passes.
const MinRationale = 8

// Reject refuses a proposal, with a rationale that is not optional.
//
// The rationale is enforced three times over: here, by a CHECK on the table, and
// by the API schema. That is not belt and braces for its own sake. This is the
// field whose absence makes the agent re-propose what was refused and the person
// conclude the tool does not listen, and it is fed straight back into assembled
// context (§6.3) — it is the most valuable single sentence in the system.
func (s *Service) Reject(ctx context.Context, id uuid.UUID, rationale string, p policy.Principal) (*pr.Proposal, error) {
	reason := strings.TrimSpace(rationale)
	if len([]rune(reason)) < MinRationale {
		return nil, fmt.Errorf("%w: say why you are refusing this. The reason is fed back into the planner's "+
			"context, and it is what stops the same proposal arriving again next week — a refusal with no reason "+
			"reads to everything downstream as though nobody looked", domain.ErrInvalid)
	}

	var out *pr.Proposal
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		prop, err := s.proposals.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.requireMember(ctx, tx, p, prop.ProjectID); err != nil {
			return err
		}
		if prop.Status.Decided() {
			return fmt.Errorf("%w: this proposal was already %s", domain.ErrConflict, prop.Status)
		}
		// Deliberately **not** gated on approval authority. Anybody on the
		// project may say "no, and here is why": refusing changes nothing, and
		// requiring a manager to be present to record a reason is how the reason
		// stops being recorded.
		ok, err := s.proposals.Decide(ctx, tx, prop.ID, repoDecision(pr.StatusRejected, p.UserID, reason, nil))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: that proposal was decided by somebody else while this request was running",
				domain.ErrConflict)
		}
		if err := audit.Write(ctx, tx, audit.Entry{
			ProjectID: &prop.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionProposalDecided, SubjectID: &prop.ID,
			Before: map[string]any{"status": string(pr.StatusPending)},
			After:  map[string]any{"status": string(pr.StatusRejected), "decision_rationale": reason},
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
		"decided_by": p.UserID, "rationale": reason,
	})
	return out, nil
}

// ── reads ────────────────────────────────────────────────────────────────────

// Proposals returns a project's proposals, without changes and so without
// conflicts. The list is a queue; the detail view is where a decision is made.
func (s *Service) Proposals(ctx context.Context, projectID uuid.UUID, status pr.Status, p policy.Principal) ([]*pr.Proposal, error) {
	if err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.proposals.ListByProject(ctx, s.db.Pool, projectID, status)
}

// Proposal returns one proposal with its changes and the conflicts it currently
// has (§7.1).
//
// Conflicts are computed rather than stored. Storing them would mean a flag that
// is correct until the next write and stale for ever after — and a reviewer
// shown "no conflicts" over a proposal that cannot land is worse off than one
// shown nothing at all.
func (s *Service) Proposal(ctx context.Context, id uuid.UUID, p policy.Principal) (*pr.Proposal, error) {
	prop, err := s.proposals.ByID(ctx, s.db.Pool, id)
	if err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, s.db.Pool, p, prop.ProjectID); err != nil {
		return nil, err
	}
	// Only a pending proposal can be stale. A decided one is history, and
	// recomputing conflicts against it would tell a reader that something
	// already applied is in conflict with what it produced.
	if prop.Status == pr.StatusPending {
		if prop.Conflicts, err = s.Conflicts(ctx, s.db.Pool, prop); err != nil {
			return nil, err
		}
	}
	return prop, nil
}

// CanApprove reports whether this principal may approve this proposal, so the
// UI can show the reason before the click rather than after it.
func (s *Service) CanApprove(ctx context.Context, prop *pr.Proposal, p policy.Principal) (bool, string) {
	err := s.mayApprove(ctx, s.db.Pool, prop, p)
	if err == nil {
		return true, ""
	}
	var den *policy.Denial
	if errors.As(err, &den) {
		return false, den.Message
	}
	return false, err.Error()
}
