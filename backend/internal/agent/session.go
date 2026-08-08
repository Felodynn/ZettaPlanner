package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	th "github.com/Felodynn/ZettaPlanner/backend/internal/domain/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/contextsvc"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
)

// Publish is a nil-safe helper so callers do not each check.
func (d *Deps) Events2(projectID uuid.UUID, kind string, payload map[string]any) {
	if d.Events != nil {
		d.Events.Publish(projectID, kind, payload)
	}
}

// newSession resolves everything a turn needs before its first model call.
func (r *Runner) newSession(ctx context.Context, turn *th.Turn) (*session, error) {
	if r.deps.Registry == nil {
		return nil, errors.New("no model registry is configured, so no turn can run")
	}
	candidates, err := r.deps.Registry.Candidates("planner")
	if err != nil || len(candidates) == 0 {
		return nil, fmt.Errorf("no model with the `tools` capability is configured for the planner role: %w", err)
	}
	// **Resolved once, here** (D22). A turn that spans two models spans two
	// context windows and two tool-calling behaviours, and neither its trace
	// nor the Phase 6 measurements can be read afterwards. Fallback across
	// `prefer` happens at the next turn boundary, not inside this one.
	client := candidates[0]

	user, err := r.deps.users.ByID(ctx, r.deps.DB.Pool, turn.RequestedBy)
	if err != nil {
		return nil, err
	}
	humanActor, err := r.deps.actors.ForUser(ctx, r.deps.DB.Pool, user.ID)
	if err != nil {
		return nil, err
	}
	agentActor, err := r.deps.actors.Agent(ctx, r.deps.DB.Pool, Name, client.Config().Model)
	if err != nil {
		return nil, err
	}

	agentID := agentActor.ID
	return &session{
		deps: &r.deps,
		turn: turn,
		principal: policy.Principal{
			UserID:      user.ID,
			ActorID:     humanActor.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			GlobalRole:  user.Role,
			IsActive:    user.IsActive,
			// This is what makes every write `agent_committed` and every audit
			// row name both the agent and the person who asked (D11).
			AgentActorID: &agentID,
		},
		requesterName: user.DisplayName,
		client:        client,
		tools:         r.tools,
		denials:       newDenialTracker(),
	}, nil
}

// openingMessages are the system prompt, the assembled context, and the prompt.
//
// **Not a replay of the thread** (D8). The model gets state, not history: a
// transcript fed back in makes every later answer a function of every earlier
// mistake, and the assembled context is the same for two people asking the same
// question in different conversations.
func (s *session) openingMessages(ctx context.Context) []llm.Message {
	prompt := s.userPrompt(ctx)

	assembled, err := s.deps.Context.Assemble(ctx, s.turn.ProjectID, contextsvc.Request{
		Message: prompt,
		Model:   s.client.Config().Name,
	}, s.principal)
	if err != nil {
		s.deps.Log.ErrorContext(ctx, "assembling context", "turn", s.turn.ID, "error", err)
		s.trace(ctx, repo.TurnEvent{Kind: repo.EventNote, Subject: "context_unavailable",
			Detail: map[string]any{"error": err.Error()}})
		return []llm.Message{
			{Role: "system", Content: systemPrompt(s.requesterName, string(s.principal.GlobalRole))},
			{Role: "user", Content: prompt},
		}
	}

	s.trace(ctx, repo.TurnEvent{
		Kind: repo.EventNote, Subject: "context_assembled",
		TokensIn: assembled.Context.Tokens,
		Detail: map[string]any{
			"budget":      assembled.Context.Budget.Tokens,
			"by_layer":    assembled.Context.ByLayer,
			"dropped":     len(assembled.Context.Drops),
			"expansion":   assembled.References.ItemRefs,
			"over_budget": assembled.Context.OverBudget(),
		},
	})

	return []llm.Message{
		{Role: "system", Content: systemPrompt(s.requesterName, string(s.principal.GlobalRole))},
		{Role: "system", Content: assembled.Text},
		{Role: "user", Content: prompt},
	}
}

// userPrompt is the message that triggered this turn.
func (s *session) userPrompt(ctx context.Context) string {
	if s.turn.MessageID == nil {
		return ""
	}
	messages, err := s.deps.messages.List(ctx, s.deps.DB.Pool, s.turn.ThreadID, repo.MessageFilter{Limit: 200})
	if err != nil {
		return ""
	}
	for _, m := range messages {
		if m.ID == *s.turn.MessageID {
			return m.Content
		}
	}
	return ""
}

// ── ending ───────────────────────────────────────────────────────────────────

// heartbeat keeps the lease alive and signals cancellation.
func (r *Runner) heartbeat(ctx context.Context, turnID uuid.UUID, done <-chan struct{}, cancelled chan<- struct{}) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			alive, err := r.deps.turns.Heartbeat(ctx, r.deps.DB.Pool, turnID)
			if err != nil {
				r.deps.Log.WarnContext(ctx, "heartbeating a turn", "turn", turnID, "error", err)
				continue
			}
			if !alive {
				// The row is no longer running: cancelled, or reaped. One
				// signal for both, because the loop's response is the same.
				close(cancelled)
				return
			}
		}
	}
}

// endTurn records the verdict and tells everyone.
func (r *Runner) endTurn(ctx context.Context, turn *th.Turn, status th.TurnStatus, finish th.FinishReason,
	errClass, detail string, started time.Time, s *session) {

	result := repo.TurnResult{
		Status: status, Finish: finish, Model: turn.Model,
		WallMS:     int(time.Since(started).Milliseconds()),
		ErrorClass: errClass, ErrorDetail: detail,
	}
	if s != nil {
		result.TokensIn, result.TokensOut = s.tokensIn, s.tokensOut
		result.Retries, result.Steps = s.retries, s.steps
	}

	// A turn cancelled underneath the loop stays cancelled: Finish is guarded
	// on `running`, so the human's verdict wins over the loop's.
	if _, err := r.deps.turns.Finish(ctx, r.deps.DB.Pool, turn.ID, result); err != nil {
		r.deps.Log.ErrorContext(ctx, "closing a turn", "turn", turn.ID, "error", err)
	}

	// Two messages, not one, and the order matters.
	//
	// The summary is what the model said it did. The note is what actually
	// happened. A `no_output` turn very often ends with a confident summary —
	// "I looked at the plan and everything seems fine" — and posting that alone
	// is precisely the failure this phase exists to close: from the outside it
	// is indistinguishable from a turn that did the work.
	if s != nil {
		if summary := strings.TrimSpace(s.summary); summary != "" {
			s.appendMessage(ctx, th.RoleAssistant, summary, nil)
		}
		if !finish.Succeeded() {
			s.appendMessage(ctx, th.RoleSystem, endNote(finish, detail), nil)
		}
	}

	action := audit.ActionTurnEnded
	switch status {
	case th.TurnAbandoned:
		action = audit.ActionTurnAbandoned
	case th.TurnCancelled:
		action = audit.ActionTurnCancelled
	}
	r.auditTurn(ctx, turn, action, map[string]any{
		"status": string(status), "finish_reason": string(finish),
		"tokens_in": result.TokensIn, "tokens_out": result.TokensOut,
		"retries": result.Retries, "wall_ms": result.WallMS,
	})

	r.publish(turn.ProjectID, "turn.ended", map[string]any{
		"turn_id": turn.ID, "status": string(status), "finish_reason": string(finish),
		"error_class": errClass, "detail": detail,
	})
	// Something may now be waiting behind this turn.
	r.Wake()
}

// endNote is what a failed turn leaves in the thread.
func endNote(finish th.FinishReason, detail string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This turn ended as `%s`: %s.", finish, th.FinishMeaning(finish))
	if detail != "" {
		fmt.Fprintf(&b, "\n\n%s", detail)
	}
	if finish == th.FinishNoOutput {
		b.WriteString("\n\nNothing was written, proposed or handed back. If the request was reasonable, " +
			"this is a harness or model failure rather than an answer — `zettaplanner trace` shows what it did.")
	}
	return b.String()
}

// ── session helpers used by the tools ────────────────────────────────────────

// item resolves a work item ref with a teaching failure.
func (s *session) item(ctx context.Context, ref string) (*plansvc.ItemDetail, error) {
	d, err := s.deps.Plan.ItemByRef(ctx, s.turn.ProjectID, ref, s.principal)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, refuse("there is no work item %s in this project. Call `get_plan` to see what exists.",
				strings.ToUpper(strings.TrimSpace(ref)))
		}
		return nil, err
	}
	return d, nil
}

// translate turns a service failure into something the model can act on.
//
// A policy denial keeps its own text verbatim — it was written to be read by an
// agent as well as by a human (§7.3) — and gains the route to this harness's
// alternative. Everything else that is the caller's fault is passed through;
// anything else is a harness failure and says so.
func (s *session) translate(err error, route string) error {
	var denial *policy.Denial
	if errors.As(err, &denial) {
		msg := denial.Message
		if route != "" {
			msg += fmt.Sprintf("\n\nFrom this turn, the route is `%s`.", route)
		}
		return &toolError{Message: msg, Denied: true}
	}
	var vocab *domain.VocabularyError
	if errors.As(err, &vocab) {
		return refuse("%s", vocab.Error())
	}
	if errors.Is(err, domain.ErrInvalid) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
		return refuse("%s", err.Error())
	}
	return err
}

// resolvePlanTarget maps a human ref onto an id and the state it is in now.
//
// `before` is captured at proposal time so Phase 7 can supersede the proposal
// if the state moved: approving a change whose premise has gone is how a plan
// silently reverts to something nobody chose.
func (s *session) resolvePlanTarget(ctx context.Context, target pr.ChangeTarget, ref string) (uuid.UUID, map[string]any, error) {
	switch target {
	case pr.ChangeWorkItem, pr.ChangeSubTask:
		d, err := s.item(ctx, ref)
		if err != nil {
			return uuid.Nil, nil, err
		}
		w := d.Item
		return w.ID, map[string]any{
			"ref": w.Ref, "title": w.Title, "status": string(w.Status),
			"priority": string(w.Priority), "start_date": pl.FormatDate(w.StartDate),
			"due_date": pl.FormatDate(w.DueDate),
		}, nil

	case pr.ChangeMilestone:
		list, err := s.deps.Plan.ListMilestones(ctx, s.turn.ProjectID, s.principal)
		if err != nil {
			return uuid.Nil, nil, err
		}
		for _, m := range list {
			if strings.EqualFold(strings.TrimSpace(m.Name), strings.TrimSpace(ref)) {
				return m.ID, map[string]any{
					"name": m.Name, "status": string(m.Status),
					"start_date": pl.FormatDate(m.StartDate), "due_date": pl.FormatDate(m.DueDate),
				}, nil
			}
		}
		var names []string
		for _, m := range list {
			names = append(names, m.Name)
		}
		return uuid.Nil, nil, refuse("there is no milestone named %q. The milestones are: %s",
			ref, orNone(strings.Join(names, ", ")))
	}
	return uuid.Nil, nil, refuse("`%s` cannot be addressed by ref here.", target)
}

// resolveNoteSubject maps a slug or ref onto a note subject.
func (s *session) resolveNoteSubject(ctx context.Context, subject string) (string, uuid.UUID, error) {
	entities, err := s.deps.Diagrams.Entities(ctx, s.turn.ProjectID, s.principal)
	if err != nil {
		return "", uuid.Nil, err
	}
	for _, e := range entities {
		if e.Slug == subject {
			return "catalog_entity", e.ID, nil
		}
	}
	if d, err := s.deps.Plan.ItemByRef(ctx, s.turn.ProjectID, subject, s.principal); err == nil {
		return "work_item", d.Item.ID, nil
	}
	return "", uuid.Nil, refuse("`%s` is neither an entity slug nor a work item ref in this project. "+
		"Omit `subject` to raise the note against the project itself.", subject)
}

// raiseProposal writes the proposal and records it.
func (s *session) raiseProposal(ctx context.Context, title, rationale string, changes []pr.Change) (Result, error) {
	p := &pr.Proposal{
		ID:         uuid.Must(uuid.NewV7()),
		ProjectID:  s.turn.ProjectID,
		ActorID:    s.principal.WritingActorID(),
		OnBehalfOf: s.principal.OnBehalfOf(),
		TurnID:     &s.turn.ID,
		Title:      title,
		Rationale:  rationale,
		Target:     pr.DeriveTarget(changes),
		Status:     pr.StatusPending,
		CreatedAt:  time.Now(),
		Changes:    changes,
	}
	if err := pr.Validate(p); err != nil {
		return Result{}, s.translate(err, "")
	}

	err := s.deps.DB.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.deps.proposals.Create(ctx, tx, p); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &p.ProjectID,
			ActorID:    p.ActorID,
			OnBehalfOf: p.OnBehalfOf,
			Action:     audit.ActionProposalCreated,
			SubjectID:  &p.ID,
			After: map[string]any{
				"title": p.Title, "rationale": p.Rationale,
				"target_kind": string(p.Target), "changes": len(p.Changes),
			},
		})
	})
	if err != nil {
		return Result{}, err
	}

	s.deps.Events2(p.ProjectID, "proposal.created", map[string]any{
		"proposal_id": p.ID, "title": p.Title, "target_kind": string(p.Target),
		"changes": len(p.Changes), "turn_id": s.turn.ID,
	})
	return Result{
		Produced: true,
		Content: fmt.Sprintf(
			"Proposal raised: %q, %d change(s) to the %s. It is pending and applies nothing until somebody with "+
				"the authority approves it. Do not raise the same proposal again in this turn.",
			p.Title, len(p.Changes), p.Target),
	}, nil
}
