package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	th "github.com/Felodynn/ZettaPlanner/backend/internal/domain/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/contextsvc"
)

// errNotFound is the sentinel the tools test against.
var errNotFound = domain.ErrNotFound

// session is one turn's state.
type session struct {
	deps *Deps
	turn *th.Turn

	// principal is the **requesting user**, carrying the agent's actor id. An
	// agent has that user's effective permissions and no more (D11): every
	// write goes through the same ownership check a human would face, and the
	// audit row names both.
	principal     policy.Principal
	requesterName string

	client  *llm.Client
	tools   *toolset
	denials *denialTracker

	produced bool
	summary  string
	handoff  string

	tokensIn  int
	tokensOut int
	retries   int
	// steps counts model calls, which is what MaxSteps bounds and what the
	// trace reports. Kept on the session rather than local to the loop so the
	// number survives into the turn row — a trace that says a two-step turn
	// took zero steps is a trace nobody trusts the rest of.
	steps int
}

// runTurn is the whole loop.
func (r *Runner) runTurn(ctx context.Context, turn *th.Turn) {
	started := time.Now()
	log := r.deps.Log.With("turn", turn.ID, "project", turn.ProjectID)

	s, err := r.newSession(ctx, turn)
	if err != nil {
		log.ErrorContext(ctx, "preparing the turn", "error", err)
		r.endTurn(ctx, turn, th.TurnFailed, th.FinishError, "harness", err.Error(), started, nil)
		return
	}

	turn.Model = s.client.Config().Model
	r.publish(turn.ProjectID, "turn.started", map[string]any{
		"turn_id": turn.ID, "requested_by": turn.RequestedBy, "model": turn.Model,
	})
	r.auditTurn(ctx, turn, audit.ActionTurnStarted, map[string]any{"model": turn.Model})

	// The heartbeat runs beside the loop rather than inside it: a model call
	// that takes four minutes must not look like a dead process, and the same
	// signal tells the loop it was cancelled.
	beat := make(chan struct{})
	cancelled := make(chan struct{})
	go r.heartbeat(ctx, turn.ID, beat, cancelled)

	finish, detail := r.loop(ctx, s, cancelled, started)
	close(beat)

	status := th.TurnComplete
	errClass := ""
	if !finish.Succeeded() {
		status = th.TurnFailed
		errClass = string(finish)
	}
	if finish == th.FinishCancelled {
		status = th.TurnCancelled
	}
	r.endTurn(ctx, turn, status, finish, errClass, detail, started, s)
}

// loop is the model/tool cycle. It returns why it stopped.
func (r *Runner) loop(ctx context.Context, s *session, cancelled <-chan struct{}, started time.Time) (th.FinishReason, string) {
	messages := s.openingMessages(ctx)
	limits := r.deps.Limits

	for {
		step := s.steps
		select {
		case <-cancelled:
			return th.FinishCancelled, "a human stopped this turn"
		default:
		}

		if reason, detail := limits.exceeded(step, s.tokensIn+s.tokensOut, time.Since(started)); reason != "" {
			s.trace(ctx, repo.TurnEvent{Kind: repo.EventLimit, Subject: string(reason),
				Detail: map[string]any{"steps": step, "tokens": s.tokensIn + s.tokensOut,
					"elapsed_ms": time.Since(started).Milliseconds()}})
			return reason, detail
		}

		s.steps++
		resp, err := s.callModel(ctx, messages)
		if err != nil {
			return th.FinishError, err.Error()
		}

		assistant := resp.Message
		messages = append(messages, assistant)
		s.persistAssistant(ctx, assistant)

		if len(assistant.ToolCalls) == 0 {
			// The model answered in prose and asked for nothing. That is the
			// end of its output — and unless something was produced earlier it
			// is a `no_output` turn, however confident the prose sounded.
			return s.verdict(), ""
		}

		var stop bool
		for _, call := range assistant.ToolCalls {
			result := s.invoke(ctx, call)
			messages = append(messages, llm.Message{
				Role: "tool", ToolName: call.Function.Name, Content: result.Content,
			})
			if result.Produced {
				s.produced = true
			}
			if result.Stop {
				stop = true
			}
		}
		if s.denials.total >= tooManyDenials {
			s.trace(ctx, repo.TurnEvent{Kind: repo.EventNote, Subject: "denial_ceiling",
				Detail: map[string]any{"denials": s.denials.total}})
			return s.verdict(), fmt.Sprintf(
				"stopped after %d refused tool calls: the boundary was not going to move", s.denials.total)
		}
		if stop {
			return s.verdict(), ""
		}
	}
}

// verdict decides between a successful turn and `no_output`.
//
// The single most important line in this package. A turn that read eight things
// and declined in prose has done nothing, and calling that success is how a
// planner earns a reputation for being agreeable and useless.
func (s *session) verdict() th.FinishReason {
	if s.produced {
		return th.FinishFinished
	}
	return th.FinishNoOutput
}

// callModel makes one call, retrying exactly the transient classes exactly once.
//
// `tool_parse`, `server`, `timeout` and nothing else. A `boundary` or
// `validation_failed` retried is a refusal rephrased, not a problem solved —
// and `context_overflow` retried is the same prompt against the same window.
// This is the gap LumixAgents Phase 6 identified: two of five golden-task
// attempts died on the model returning nothing usable and neither was retried.
func (s *session) callModel(ctx context.Context, messages []llm.Message) (*llm.ChatResponse, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			s.retries++
			s.trace(ctx, repo.TurnEvent{
				Kind: repo.EventModelRetry, Subject: s.client.Config().Name,
				ErrorClass: string(llm.ClassOf(lastErr)),
				Detail:     map[string]any{"attempt": attempt + 1, "because": lastErr.Error()},
			})
		}

		call := time.Now()
		resp, err := s.client.ChatStream(ctx, llm.ChatRequest{
			Messages: messages,
			Tools:    s.tools.offered(),
		}, s.streamChunk(ctx))
		took := time.Since(call)

		if err == nil {
			s.tokensIn += resp.PromptEvalCount
			s.tokensOut += resp.EvalCount
			s.trace(ctx, repo.TurnEvent{
				Kind: repo.EventModelCall, Subject: resp.Model,
				DurationMS: int(took.Milliseconds()),
				TokensIn:   resp.PromptEvalCount, TokensOut: resp.EvalCount,
				Detail: map[string]any{
					"tool_calls": toolNames(resp.Message.ToolCalls),
					"done":       resp.DoneReason,
				},
			})
			return resp, nil
		}

		lastErr = err
		class := llm.ClassOf(err)
		s.trace(ctx, repo.TurnEvent{
			Kind: repo.EventModelError, Subject: s.client.Config().Name,
			ErrorClass: string(class), DurationMS: int(took.Milliseconds()),
			Detail: map[string]any{"error": err.Error(), "retryable": class.Retryable()},
		})
		if !class.Retryable() {
			return nil, err
		}
	}
	return nil, lastErr
}

// streamChunk forwards partial content as `message.delta` (§7.2).
func (s *session) streamChunk(ctx context.Context) func(llm.ChatResponse) error {
	if s.deps.Events == nil {
		return nil
	}
	return func(chunk llm.ChatResponse) error {
		if chunk.Message.Content == "" {
			return nil
		}
		s.deps.Events.Publish(s.turn.ProjectID, "message.delta", map[string]any{
			"turn_id": s.turn.ID, "text": chunk.Message.Content,
		})
		return ctx.Err()
	}
}

// invoke runs one tool call and records everything about it.
func (s *session) invoke(ctx context.Context, call llm.ToolCall) Result {
	name := call.Function.Name
	s.trace(ctx, repo.TurnEvent{
		Kind: repo.EventToolCall, Subject: name,
		Detail: map[string]any{"arguments": call.Function.Arguments},
	})

	tool, known := s.tools.get(name)
	if !known {
		count := s.denials.record(name)
		msg := unknownTool(name, s.tools.names()) + escalation(name, count)
		s.trace(ctx, repo.TurnEvent{Kind: repo.EventToolUnknown, Subject: name,
			Detail: map[string]any{"answer": msg}})
		return Result{Content: msg, Denied: true}
	}

	call.Function.Arguments = orEmptyArgs(call.Function.Arguments)
	begin := time.Now()
	result, err := tool.Run(ctx, s, args(call.Function.Arguments))
	took := int(time.Since(begin).Milliseconds())

	if err != nil {
		var te *toolError
		if !errors.As(err, &te) {
			// A harness failure, not a refusal. The model is told plainly and
			// without a route, because there is nothing it can do differently.
			s.trace(ctx, repo.TurnEvent{Kind: repo.EventToolResult, Subject: name,
				ErrorClass: "harness", DurationMS: took,
				Detail: map[string]any{"error": err.Error()}})
			s.deps.Log.ErrorContext(ctx, "tool failed", "tool", name, "error", err)
			return Result{Content: "That failed inside the harness rather than because of anything you did: " +
				err.Error() + "\nDo not retry it."}
		}

		content := te.Message
		kind := repo.EventToolResult
		if te.Denied {
			count := s.denials.record(name)
			content += escalation(name, count)
			kind = repo.EventToolDenied
		}
		s.trace(ctx, repo.TurnEvent{Kind: kind, Subject: name, DurationMS: took,
			Detail: map[string]any{"answer": content}})
		s.persistTool(ctx, name, content, outcomeFor(te))
		return Result{Content: content, Denied: te.Denied}
	}

	s.trace(ctx, repo.TurnEvent{Kind: repo.EventToolResult, Subject: name, DurationMS: took,
		Detail: map[string]any{"produced": result.Produced, "answer": truncate(result.Content, 2000)}})
	s.persistTool(ctx, name, result.Content, "ok")
	if result.Produced {
		s.deps.Events2(s.turn.ProjectID, "plan.changed", map[string]any{
			"subject_kind": "turn", "subject_id": s.turn.ID, "tool": name,
		})
	}
	return result
}

func outcomeFor(te *toolError) string {
	if te.Denied {
		return "denied"
	}
	return "error"
}

// ── persistence ──────────────────────────────────────────────────────────────

func (s *session) persistAssistant(ctx context.Context, m llm.Message) {
	calls := make([]th.ToolCall, 0, len(m.ToolCalls))
	for _, c := range m.ToolCalls {
		calls = append(calls, th.ToolCall{Name: c.Function.Name, Arguments: c.Function.Arguments})
	}
	if strings.TrimSpace(m.Content) == "" && len(calls) == 0 {
		return
	}
	s.appendMessage(ctx, th.RoleAssistant, m.Content, calls)
	if strings.TrimSpace(m.Content) != "" {
		s.deps.Events2(s.turn.ProjectID, "message.complete", map[string]any{
			"turn_id": s.turn.ID, "role": "assistant", "content": m.Content,
		})
	}
}

func (s *session) persistTool(ctx context.Context, name, content, outcome string) {
	s.appendMessage(ctx, th.RoleTool, content, []th.ToolCall{{Name: name, Outcome: outcome}})
}

func (s *session) appendMessage(ctx context.Context, role th.Role, content string, calls []th.ToolCall) {
	turnID := s.turn.ID
	m := &th.Message{
		ID:        uuid.Must(uuid.NewV7()),
		ThreadID:  s.turn.ThreadID,
		ActorID:   s.principal.WritingActorID(),
		Role:      role,
		Content:   content,
		ToolCalls: calls,
		TurnID:    &turnID,
		CreatedAt: time.Now(),
	}
	if err := s.deps.messages.Append(ctx, s.deps.DB.Pool, m); err != nil {
		s.deps.Log.ErrorContext(ctx, "persisting a turn message", "turn", s.turn.ID, "error", err)
	}
}

// trace appends one row to the turn's trace.
//
// Failures here are logged and swallowed. A trace write that fails must not
// take a turn down with it — the turn is the work, the trace is the record of
// it, and losing the record of a successful turn is better than losing the turn.
func (s *session) trace(ctx context.Context, e repo.TurnEvent) {
	e.ID = uuid.Must(uuid.NewV7())
	e.TurnID = s.turn.ID
	e.At = time.Now()
	if err := s.deps.turns.AppendEvent(ctx, s.deps.DB.Pool, &e); err != nil {
		s.deps.Log.WarnContext(ctx, "appending to the turn trace", "turn", s.turn.ID, "error", err)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func toolNames(calls []llm.ToolCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Function.Name)
	}
	return out
}

func orEmptyArgs(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "… [truncated in the trace]"
}

var _ = contextsvc.Request{}
