package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	th "github.com/Felodynn/ZettaPlanner/backend/internal/domain/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	threadsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/thread"
)

// ── wire types ───────────────────────────────────────────────────────────────

// ThreadSummary is one conversation.
type ThreadSummary struct {
	ID            uuid.UUID  `json:"id"`
	ScopeKind     string     `json:"scope_kind" enum:"project,work_item,diagram"`
	ScopeID       string     `json:"scope_id,omitempty"`
	ScopeLabel    string     `json:"scope_label,omitempty" doc:"The item ref or diagram name this thread is about"`
	MessageCount  int        `json:"message_count"`
	LastMessageAt *time.Time `json:"last_message_at,omitempty"`
}

// MessageSummary is one entry in a transcript.
type MessageSummary struct {
	ID        uuid.UUID        `json:"id"`
	Seq       int64            `json:"seq"`
	Role      string           `json:"role" enum:"user,assistant,tool,system"`
	Content   string           `json:"content"`
	ToolCalls []ToolCallDetail `json:"tool_calls,omitempty"`
	TurnID    string           `json:"turn_id,omitempty"`
	ActorName string           `json:"actor_name,omitempty"`
	CreatedAt time.Time        `json:"created_at"`
}

// ToolCallDetail is one call the model asked for.
type ToolCallDetail struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome,omitempty" doc:"ok, denied, error or unknown"`
}

// TurnSummary is one agent run.
type TurnSummary struct {
	ID           uuid.UUID `json:"id"`
	ThreadID     uuid.UUID `json:"thread_id"`
	Status       string    `json:"status" enum:"queued,running,complete,failed,abandoned,cancelled"`
	FinishReason string    `json:"finish_reason,omitempty" doc:"Why it ended. no_output means it produced no write, no proposal and no handoff — a non-success, not an answer"`
	// Position is 0 for anything not queued, 1 for the next in line. §5.3
	// requires a queued turn to be shown as queued rather than merely to be.
	Position    int        `json:"queue_position"`
	RequestedBy string     `json:"requested_by,omitempty"`
	Model       string     `json:"model,omitempty"`
	TokensIn    int        `json:"tokens_in"`
	TokensOut   int        `json:"tokens_out"`
	Steps       int        `json:"steps"`
	Retries     int        `json:"retries"`
	WallMS      int        `json:"wall_ms"`
	ErrorClass  string     `json:"error_class,omitempty"`
	ErrorDetail string     `json:"error_detail,omitempty"`
	QueuedAt    time.Time  `json:"queued_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
}

// ProposalSummary is one governed change set.
type ProposalSummary struct {
	ID                uuid.UUID        `json:"id"`
	Title             string           `json:"title"`
	Rationale         string           `json:"rationale"`
	TargetKind        string           `json:"target_kind" enum:"diagram,plan,mixed"`
	Status            string           `json:"status" enum:"pending,approved,rejected,superseded"`
	RaisedBy          string           `json:"raised_by,omitempty"`
	OnBehalfOf        string           `json:"on_behalf_of,omitempty" doc:"The instructing user when an agent raised it"`
	TurnID            string           `json:"turn_id,omitempty"`
	DecidedBy         string           `json:"decided_by,omitempty"`
	DecisionRationale string           `json:"decision_rationale,omitempty"`
	CreatedAt         time.Time        `json:"created_at"`
	Changes           []ProposalChange `json:"changes"`
}

// ProposalChange is one typed edit inside a proposal.
type ProposalChange struct {
	Seq      int            `json:"seq"`
	Op       string         `json:"op" enum:"create,update,delete"`
	Target   string         `json:"target"`
	TargetID string         `json:"target_id,omitempty"`
	Before   map[string]any `json:"before,omitempty" doc:"The state when this was raised. Phase 7 supersedes the proposal if it has moved"`
	After    map[string]any `json:"after,omitempty"`
}

// ── outputs ──────────────────────────────────────────────────────────────────

type threadListOutput struct {
	Body struct {
		Threads []ThreadSummary `json:"threads"`
	}
}

type threadOutput struct{ Body ThreadSummary }

type messageListOutput struct {
	Body struct {
		Messages []MessageSummary `json:"messages"`
	}
}

type postMessageOutput struct {
	Body struct {
		Message MessageSummary `json:"message"`
		Turn    TurnSummary    `json:"turn"`
	}
}

type turnListOutput struct {
	Body struct {
		Turns []TurnSummary `json:"turns"`
	}
}

type turnOutput struct{ Body TurnSummary }

type proposalListOutput struct {
	Body struct {
		Proposals []ProposalSummary `json:"proposals"`
	}
}

type proposalOutput struct{ Body ProposalSummary }

// ── registration ─────────────────────────────────────────────────────────────

func (s *Server) registerThreads() {
	huma.Register(s.API, huma.Operation{
		OperationID: "list-threads",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/threads",
		Summary:     "A project's conversations",
		Description: "Every thread is readable by every project member, including managers reading developers' turns. " +
			"Scope controls context assembly and UI placement, never visibility (§3.6).",
		Tags: []string{"conversation"},
	}, func(ctx context.Context, in *struct {
		PID uuid.UUID `path:"pid"`
	}) (*threadListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Threads.List(ctx, in.PID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &threadListOutput{}
		out.Body.Threads = []ThreadSummary{}
		for _, t := range list {
			out.Body.Threads = append(out.Body.Threads, threadSummary(t))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "ensure-thread",
		Method:        http.MethodPost,
		Path:          "/api/v1/projects/{pid}/threads",
		Summary:       "Open the thread for a scope, creating it on first use",
		Description:   "Idempotent: there is exactly one thread per scope, and asking twice returns the same one.",
		Tags:          []string{"conversation"},
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, in *struct {
		PID  uuid.UUID `path:"pid"`
		Body struct {
			ScopeKind string `json:"scope_kind,omitempty" enum:"project,work_item,diagram"`
			ScopeRef  string `json:"scope_ref,omitempty" doc:"A work item ref (ZP-14) or a diagram slug. Omit for the project thread"`
		}
	}) (*threadOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		t, err := s.deps.Threads.Ensure(ctx, in.PID, threadsvc.EnsureInput{
			ScopeKind: th.ScopeKind(in.Body.ScopeKind),
			ScopeRef:  in.Body.ScopeRef,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &threadOutput{Body: threadSummary(t)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "list-messages",
		Method:      http.MethodGet,
		Path:        "/api/v1/threads/{id}/messages",
		Summary:     "A thread's transcript",
		Description: "Chat is transcript, never memory (D8): nothing here is replayed to a model. A turn receives " +
			"assembled state and the current prompt.",
		Tags: []string{"conversation"},
	}, func(ctx context.Context, in *struct {
		ID     uuid.UUID `path:"id"`
		Before int64     `query:"before" doc:"Return the messages immediately preceding this sequence number"`
		Limit  int       `query:"limit" minimum:"1" maximum:"500"`
		Turn   string    `query:"turn" doc:"Restrict to one turn"`
	}) (*messageListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		f := repo.MessageFilter{Limit: in.Limit}
		if in.Before > 0 {
			before := in.Before
			f.Before = &before
		}
		if in.Turn != "" {
			id, err := uuid.Parse(in.Turn)
			if err != nil {
				return nil, s.mapErr(ctx, ValidationFailed("turn=%q is not a turn id.", in.Turn))
			}
			f.TurnID = &id
		}
		list, err := s.deps.Threads.Messages(ctx, in.ID, f, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &messageListOutput{}
		out.Body.Messages = []MessageSummary{}
		for _, m := range list {
			out.Body.Messages = append(out.Body.Messages, messageSummary(m))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "post-message",
		Method:      http.MethodPost,
		Path:        "/api/v1/threads/{id}/messages",
		Summary:     "Post a message and enqueue an agent turn",
		Description: "The message always lands. The turn queues behind whatever else is running on the project " +
			"(§5.3) and the response carries its position — a second user's prompt is queued and shown as queued, " +
			"never run concurrently.",
		Tags:          []string{"conversation"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body struct {
			Content string `json:"content" minLength:"1"`
		}
	}) (*postMessageOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		res, err := s.deps.Threads.Post(ctx, in.ID, in.Body.Content, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &postMessageOutput{}
		out.Body.Message = messageSummary(res.Message)
		out.Body.Turn = turnSummary(res.Turn)
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "list-turns",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/turns",
		Summary:     "A project's agent turns, newest first",
		Tags:        []string{"conversation"},
	}, func(ctx context.Context, in *struct {
		PID   uuid.UUID `path:"pid"`
		Limit int       `query:"limit" minimum:"1" maximum:"200"`
	}) (*turnListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Threads.Turns(ctx, in.PID, in.Limit, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &turnListOutput{}
		out.Body.Turns = []TurnSummary{}
		for _, t := range list {
			out.Body.Turns = append(out.Body.Turns, turnSummary(t))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "get-turn",
		Method:      http.MethodGet,
		Path:        "/api/v1/turns/{id}",
		Summary:     "One turn, with its outcome and its cost",
		Tags:        []string{"conversation"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*turnOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		t, err := s.deps.Threads.Turn(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &turnOutput{Body: turnSummary(t)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "cancel-turn",
		Method:      http.MethodPost,
		Path:        "/api/v1/turns/{id}/cancel",
		Summary:     "Stop a queued or running turn",
		Description: "Anybody on the project may cancel. A turn holds the project's only slot, and requiring the " +
			"person who started it to be present is how a queue stays blocked all afternoon. " +
			"Cancellation is a real outcome, not a flavour of failure.",
		Tags: []string{"conversation"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*turnOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		t, err := s.deps.Threads.Cancel(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &turnOutput{Body: turnSummary(t)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "list-proposals",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/proposals",
		Summary:     "A project's proposals",
		Description: "Phase 6 raises them; approving and applying arrives in Phase 7. A pending proposal has " +
			"changed nothing.",
		Tags: []string{"governance"},
	}, func(ctx context.Context, in *struct {
		PID    uuid.UUID `path:"pid"`
		Status string    `query:"status" enum:"pending,approved,rejected,superseded"`
	}) (*proposalListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Threads.Proposals(ctx, in.PID, pr.Status(in.Status), p)
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
		Summary:     "One proposal with its ordered changes",
		Tags:        []string{"governance"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*proposalOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		prop, err := s.deps.Threads.Proposal(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &proposalOutput{Body: proposalSummary(prop)}, nil
	})
}

// ── renderers ────────────────────────────────────────────────────────────────

func threadSummary(t *th.Thread) ThreadSummary {
	out := ThreadSummary{
		ID: t.ID, ScopeKind: string(t.ScopeKind), ScopeLabel: t.ScopeLabel,
		MessageCount: t.MessageCount, LastMessageAt: t.LastMessageAt,
	}
	if t.ScopeID != nil {
		out.ScopeID = t.ScopeID.String()
	}
	return out
}

func messageSummary(m *th.Message) MessageSummary {
	out := MessageSummary{
		ID: m.ID, Seq: m.Seq, Role: string(m.Role), Content: m.Content,
		ActorName: m.ActorName, CreatedAt: m.CreatedAt,
	}
	if m.TurnID != nil {
		out.TurnID = m.TurnID.String()
	}
	for _, c := range m.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCallDetail{Name: c.Name, Outcome: c.Outcome})
	}
	return out
}

func turnSummary(t *th.Turn) TurnSummary {
	return TurnSummary{
		ID: t.ID, ThreadID: t.ThreadID, Status: string(t.Status), FinishReason: string(t.Finish),
		Position: t.QueuePosition, RequestedBy: t.RequestedByName, Model: t.Model,
		TokensIn: t.TokensIn, TokensOut: t.TokensOut, Steps: t.Steps, Retries: t.Retries,
		WallMS: t.WallMS, ErrorClass: t.ErrorClass, ErrorDetail: t.ErrorDetail,
		QueuedAt: t.QueuedAt, StartedAt: t.StartedAt, EndedAt: t.EndedAt,
	}
}

func proposalSummary(p *pr.Proposal) ProposalSummary {
	out := ProposalSummary{
		ID: p.ID, Title: p.Title, Rationale: p.Rationale,
		TargetKind: string(p.Target), Status: string(p.Status),
		RaisedBy: p.RaisedByName, DecidedBy: p.DecidedByName,
		DecisionRationale: p.DecisionRationale, CreatedAt: p.CreatedAt,
		Changes: []ProposalChange{},
	}
	if p.OnBehalfOf != nil {
		out.OnBehalfOf = p.OnBehalfOf.String()
	}
	if p.TurnID != nil {
		out.TurnID = p.TurnID.String()
	}
	for _, c := range p.Changes {
		change := ProposalChange{
			Seq: c.Seq, Op: string(c.Op), Target: string(c.Target),
			Before: c.Before, After: c.After,
		}
		if c.TargetID != nil {
			change.TargetID = c.TargetID.String()
		}
		out.Changes = append(out.Changes, change)
	}
	return out
}

// principalFromRequest is the SSE endpoint's gate. It bypasses huma, so it
// reads the principal the session middleware put in the context directly.
func principalFromRequest(ctx context.Context) (policy.Principal, bool) {
	return policy.PrincipalFrom(ctx)
}
