package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
)

// Governance's write surface: approve, reject, and re-evaluate.
//
// The read routes live here too rather than with the conversation, because
// §7.1 asks GET /proposals/{id} for "changes + **current conflicts**" and the
// conflicts come from the service that knows how to apply them. A reader shown
// a diff with no indication that it cannot land is worse off than one shown
// nothing at all.

// ProposalConflict is one recorded premise that no longer holds.
type ProposalConflict struct {
	Seq      int    `json:"seq" doc:"Which change in the proposal"`
	Target   string `json:"target"`
	Element  string `json:"element" doc:"What a human calls it: ZP-14, redis-cache, the milestone name"`
	Field    string `json:"field,omitempty"`
	Expected string `json:"expected,omitempty" doc:"What it held when the proposal was raised"`
	Actual   string `json:"actual,omitempty" doc:"What it holds now"`
	Reason   string `json:"reason,omitempty" doc:"Set instead of field when the whole target is the problem"`
}

type rejectInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		// Required, and the API is the third place that says so after the
		// service and a CHECK on the table (§5.2). The rationale is fed back
		// into assembled context; without it the same proposal arrives again
		// next week and the person concludes the tool does not listen.
		Rationale string `json:"rationale" minLength:"8" doc:"Why you are refusing. Required — it becomes context the planner reads"`
	}
}

type reevaluateOutput struct {
	Body struct {
		Proposal  *ProposalSummary `json:"proposal,omitempty" doc:"Absent when the model found nothing to change, which is a real answer"`
		PagesRead int              `json:"pages_read" doc:"Source pages re-read, one model call each. Zero means the diagram has no ingest behind it"`
		Model     string           `json:"model,omitempty"`
		Skipped   string           `json:"skipped,omitempty" doc:"What was deliberately left out of this run"`
		Message   string           `json:"message"`
	}
}

func (s *Server) registerProposals() {
	huma.Register(s.API, huma.Operation{
		OperationID: "list-proposals",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/proposals",
		Summary:     "A project's proposals",
		Description: "The review queue. A pending proposal has changed nothing. Conflicts are computed per " +
			"proposal on the detail route rather than here, because computing them for a whole list means " +
			"reading every target the project has.",
		Tags: []string{"governance"},
	}, func(ctx context.Context, in *struct {
		PID    uuid.UUID `path:"pid"`
		Status string    `query:"status" enum:"pending,approved,rejected,superseded"`
	}) (*proposalListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Governance.Proposals(ctx, in.PID, pr.Status(in.Status), p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &proposalListOutput{}
		out.Body.Proposals = []ProposalSummary{}
		for _, prop := range list {
			out.Body.Proposals = append(out.Body.Proposals, proposalSummary(prop))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "get-proposal",
		Method:      http.MethodGet,
		Path:        "/api/v1/proposals/{id}",
		Summary:     "One proposal with its ordered changes and its current conflicts",
		Description: "`conflicts` is computed now, not stored. A non-empty list means approving this would " +
			"supersede it rather than apply it: the state it was raised against has moved, and reconciling " +
			"the two is the judgement you were being asked to make.",
		Tags: []string{"governance"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*proposalOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		prop, err := s.deps.Governance.Proposal(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		body := proposalSummary(prop)
		if prop.Status == pr.StatusPending {
			ok, why := s.deps.Governance.CanApprove(ctx, prop, p)
			body.CanApprove = ok
			body.ApprovalBlockedBecause = why
		}
		return &proposalOutput{Body: body}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "approve-proposal",
		Method:      http.MethodPost,
		Path:        "/api/v1/proposals/{id}/approve",
		Summary:     "Approve a proposal and apply it as one revision",
		Description: "Atomic: every change lands or none does. If any change's recorded premise has moved, the " +
			"proposal becomes `superseded`, the conflict is named, and **nothing is applied** — never partially, " +
			"never auto-merged.\n\n" +
			"Managers and admins may approve anything. A developer may approve only a proposal whose every " +
			"change is one they could have written directly.",
		Tags: []string{"governance"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*proposalOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		prop, err := s.deps.Governance.Approve(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &proposalOutput{Body: proposalSummary(prop)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "reject-proposal",
		Method:      http.MethodPost,
		Path:        "/api/v1/proposals/{id}/reject",
		Summary:     "Refuse a proposal, with a rationale",
		Description: "The rationale is required. It is fed into assembled context (§5.2), which is what stops " +
			"the planner proposing the same thing again — and what stops the person concluding the tool does " +
			"not listen.\n\n" +
			"Anybody on the project may refuse. Refusing changes nothing, and requiring a manager to be " +
			"present to record a reason is how the reason stops being recorded.",
		Tags: []string{"governance"},
	}, func(ctx context.Context, in *rejectInput) (*proposalOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		prop, err := s.deps.Governance.Reject(ctx, in.ID, in.Body.Rationale, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &proposalOutput{Body: proposalSummary(prop)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "reevaluate-diagram",
		Method:      http.MethodPost,
		Path:        "/api/v1/diagrams/{id}/reevaluate",
		Summary:     "Ask the vision model to look at this diagram again",
		Description: "**Returns a Proposal, never a write** (§5.4) — including for a manager who could have " +
			"written it directly. A stray click must not rewrite a diagram somebody corrected by hand.\n\n" +
			"The model is shown the source pages this diagram was ingested from, one call per page, alongside " +
			"the current graph with **per-element provenance**: what a human created or edited is presented " +
			"as a decision rather than a candidate, and any correction that would overwrite one is dropped " +
			"and reported in the rationale. A diagram with no ingest behind it is re-read from its graph alone.\n\n" +
			"Synchronous, and slow on a multi-page document.",
		Tags: []string{"governance", "diagram"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*reevaluateOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		res, err := s.deps.Governance.Reevaluate(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &reevaluateOutput{}
		out.Body.PagesRead = res.PagesRead
		out.Body.Model = res.Model
		out.Body.Skipped = res.Skipped
		if res.Proposal == nil {
			out.Body.Message = "The model found nothing to change. That is a real answer, and a common one for " +
				"a diagram somebody has already corrected."
			return out, nil
		}
		summary := proposalSummary(res.Proposal)
		out.Body.Proposal = &summary
		out.Body.Message = "Raised as a proposal. Nothing has been applied."
		return out, nil
	})
}

func renderConflicts(in []pr.Conflict) []ProposalConflict {
	out := make([]ProposalConflict, 0, len(in))
	for _, c := range in {
		out = append(out, ProposalConflict{
			Seq: c.Seq, Target: string(c.Target), Element: c.Element,
			Field: c.Field, Expected: c.Expected, Actual: c.Actual, Reason: c.Reason,
		})
	}
	return out
}
