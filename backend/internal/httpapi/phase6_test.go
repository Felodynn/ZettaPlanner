package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/agent"
	"github.com/Felodynn/ZettaPlanner/backend/internal/config"
	"github.com/Felodynn/ZettaPlanner/backend/internal/httpapi"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/auth"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/contextsvc"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/governance"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/project"
	threadsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/testdb"
)

// Phase 6's exit criteria, over the real HTTP surface, a real Postgres, and a
// scripted model.
//
// Scripted rather than real on purpose: Phase 6 is testing the harness, not the
// model. "Does the tool fire, is the policy enforced, is the audit row written,
// does the turn terminate" are properties of this code, and a real model makes
// them non-deterministic without making them better tested.

// ── a scripted model ─────────────────────────────────────────────────────────

// scriptedModel answers /api/chat with whatever the test queued, in order.
type scriptedModel struct {
	srv *httptest.Server

	mu       sync.Mutex
	steps    []scriptStep
	at       int
	requests []scriptRequest
}

// scriptStep is one model reply, or one failure.
type scriptStep struct {
	// Status, when non-zero, makes the call fail with that HTTP status instead
	// of answering. This is how the retry policy is exercised.
	Status int
	// Content is the assistant's prose.
	Content string
	// Calls are the tools it asks for.
	Calls []scriptCall
}

type scriptCall struct {
	Name string
	Args map[string]any
}

type scriptRequest struct {
	tools    []string
	messages []map[string]any
}

func newScriptedModel(t *testing.T) *scriptedModel {
	t.Helper()
	m := &scriptedModel{}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
			Tools    []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Options struct {
				NumCtx int `json:"num_ctx"`
			} `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("scripted model: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// D4: num_ctx on every request, or the window is whatever the server
		// felt like. Asserted here so a regression is a test failure rather
		// than an agent with amnesia six months later.
		if body.Options.NumCtx == 0 {
			t.Error("scripted model: request carried no num_ctx")
		}

		var names []string
		for _, tool := range body.Tools {
			names = append(names, tool.Function.Name)
		}

		m.mu.Lock()
		m.requests = append(m.requests, scriptRequest{tools: names, messages: body.Messages})
		var step scriptStep
		if m.at < len(m.steps) {
			step = m.steps[m.at]
			m.at++
		} else {
			// Ran off the end of the script: answer in prose so the loop ends
			// rather than spinning against an empty queue.
			step = scriptStep{Content: "(the script ran out)"}
		}
		m.mu.Unlock()

		if step.Status != 0 {
			w.WriteHeader(step.Status)
			_, _ = w.Write([]byte(`{"error":"scripted failure"}`))
			return
		}

		msg := map[string]any{"role": "assistant", "content": step.Content}
		if len(step.Calls) > 0 {
			calls := make([]map[string]any, 0, len(step.Calls))
			for _, c := range step.Calls {
				args := c.Args
				if args == nil {
					args = map[string]any{}
				}
				calls = append(calls, map[string]any{
					"function": map[string]any{"name": c.Name, "arguments": args},
				})
			}
			msg["tool_calls"] = calls
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "scripted", "created_at": time.Now(), "message": msg,
			"done": true, "done_reason": "stop",
			"prompt_eval_count": 100, "eval_count": 20,
		})
	})
	mux.HandleFunc("/api/embed", func(w http.ResponseWriter, r *http.Request) {
		// No embedding model in these tests; retrieval reports itself
		// unavailable, which the agent's search tool is expected to survive.
		w.WriteHeader(http.StatusNotFound)
	})

	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func (m *scriptedModel) script(steps ...scriptStep) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.steps = append(m.steps, steps...)
}

func (m *scriptedModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func (m *scriptedModel) lastTools() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return nil
	}
	return m.requests[len(m.requests)-1].tools
}

func (m *scriptedModel) registry(t *testing.T) *llm.Registry {
	t.Helper()
	cfg := &config.Models{
		Models: map[string]*config.Model{
			"planner-test": {
				Name: "planner-test", Endpoint: m.srv.URL, Tier: config.TierSelfHosted,
				Model: "scripted", NumCtx: 8192, AssemblyBudget: 4000,
				Caps: []config.Cap{config.CapTools}, Concurrency: 1,
			},
			// The same scripted server under a second name, so re-evaluate
			// (Phase 7) resolves a vision model without a second fixture. The
			// script queue is shared, which is exactly right: a test that runs
			// a turn and then a re-evaluation wants both answers in the order
			// it wrote them.
			"vision-test": {
				Name: "vision-test", Endpoint: m.srv.URL, Tier: config.TierSelfHosted,
				Model: "scripted", NumCtx: 8192, AssemblyBudget: 4000,
				Caps: []config.Cap{config.CapVision}, Concurrency: 1,
			},
		},
		Roles: map[string]*config.RoleSpec{
			"planner": {Requires: []config.Cap{config.CapTools}, Prefer: []string{"planner-test"}},
			"vision":  {Requires: []config.Cap{config.CapVision}, Prefer: []string{"vision-test"}},
		},
	}
	return llm.NewRegistry(cfg)
}

// ── the fixture ──────────────────────────────────────────────────────────────

type agentFixture struct {
	*planFixture
	model  *scriptedModel
	runner *agent.Runner
	svc    *threadsvc.Service
	ctx    context.Context
	thread uuid.UUID
}

// newAgentFixture rebuilds the whole server with an agent wired in.
//
// A separate constructor rather than an option on newHarness: the runner takes
// a model registry and a background goroutine, and every other phase's tests
// are better off without either.
func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	return newAgentFixtureWithLimits(t, agent.Limits{})
}

func newAgentFixtureWithLimits(t *testing.T, limits agent.Limits) *agentFixture {
	t.Helper()

	db, ctx := testdb.Fresh(t, "agent")
	quiet := slog.New(slog.DiscardHandler)
	if _, err := store.MigrateUp(ctx, db, quiet); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	model := newScriptedModel(t)
	events := httpapi.NewEventHub()
	contextSvc := contextsvc.New(db, quiet, nil)
	planSvc := plansvc.New(db, quiet, contextSvc)
	diagramSvc := diagramsvc.New(db, quiet, 0)

	runner := agent.New(agent.Deps{
		DB: db, Log: quiet, Registry: model.registry(t),
		Context: contextSvc, Plan: planSvc, Diagrams: diagramSvc, Events: events,
		Limits: limits,
	})
	threadSvc := threadsvc.New(db, quiet, runner, events)
	governanceSvc := governance.New(db, quiet, planSvc, diagramSvc, events, model.registry(t))

	api := httpapi.New(httpapi.Deps{
		DB: db, Auth: auth.New(db, quiet, 0), Projects: project.New(db, quiet),
		Diagrams: diagramSvc, Plan: planSvc, Context: contextSvc, Threads: threadSvc,
		Governance: governanceSvc, Events: events, Log: quiet,
	})
	srv := httptest.NewServer(api.Handler)
	t.Cleanup(func() { api.Close(); srv.Close() })

	h := &harness{t: t, srv: srv, db: db}
	base := buildPlanFixture(t, h)

	runner.Start(context.Background())
	t.Cleanup(runner.Stop)

	f := &agentFixture{
		planFixture: base, model: model, runner: runner, svc: threadSvc, ctx: ctx,
	}
	var thread struct {
		ID uuid.UUID `json:"id"`
	}
	f.h.decode(f.dev.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/threads",
		map[string]any{"scope_kind": "project"}), http.StatusOK, &thread)
	f.thread = thread.ID
	return f
}

// post sends a message and waits for its turn to end.
func (f *agentFixture) post(c *client, content string) turnView {
	f.h.t.Helper()
	var out struct {
		Turn turnView `json:"turn"`
	}
	f.h.decode(c.do(http.MethodPost, "/api/v1/threads/"+f.thread.String()+"/messages",
		map[string]any{"content": content}), http.StatusCreated, &out)
	return f.await(out.Turn.ID)
}

type turnView struct {
	ID           uuid.UUID `json:"id"`
	Status       string    `json:"status"`
	FinishReason string    `json:"finish_reason"`
	Position     int       `json:"queue_position"`
	Retries      int       `json:"retries"`
	ErrorClass   string    `json:"error_class"`
	ErrorDetail  string    `json:"error_detail"`
	Model        string    `json:"model"`
}

func (f *agentFixture) await(id uuid.UUID) turnView {
	f.h.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var t turnView
		f.h.decode(f.dev.do(http.MethodGet, "/api/v1/turns/"+id.String(), nil), http.StatusOK, &t)
		if t.Status != "queued" && t.Status != "running" {
			return t
		}
		time.Sleep(25 * time.Millisecond)
	}
	f.h.t.Fatalf("turn %s never ended", id)
	return turnView{}
}

func (f *agentFixture) proposals() []map[string]any {
	f.h.t.Helper()
	var out struct {
		Proposals []map[string]any `json:"proposals"`
	}
	f.h.decode(f.dev.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/proposals", nil),
		http.StatusOK, &out)
	return out.Proposals
}

func (f *agentFixture) traceKinds(id uuid.UUID) []string {
	f.h.t.Helper()
	rows, err := f.h.db.Pool.Query(context.Background(),
		`SELECT kind FROM turn_events WHERE turn_id = $1 ORDER BY seq`, id)
	if err != nil {
		f.h.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			f.h.t.Fatal(err)
		}
		out = append(out, k)
	}
	return out
}

// ── the exit criteria ────────────────────────────────────────────────────────

// TestNoToolReachesAManagerOwnedField is the first exit criterion, checked
// against the inventory rather than against behaviour.
//
// The strongest form of the guarantee: not "the agent was refused" but "there
// is nothing to call". A prompt is a request; an inventory is a fact.
func TestNoToolReachesAManagerOwnedField(t *testing.T) {
	runner := agent.New(agent.Deps{Log: slog.New(slog.DiscardHandler)})

	// Generated from the ownership table rather than from a hand-written list,
	// so a field moved from one column to the other tomorrow is covered by this
	// test tomorrow, without anybody remembering to extend it.
	for _, tool := range runner.Tools() {
		if tool.Kind != agent.KindWrite {
			continue
		}
		if tool.Object == "" {
			t.Errorf("write tool %s does not say what it writes, so nothing can check it", tool.Name)
			continue
		}
		rules, all, ok := policy.Rules(tool.Object)
		if !ok {
			t.Errorf("write tool %s writes %q, which is not in the ownership table", tool.Name, tool.Object)
			continue
		}
		if all != nil {
			t.Errorf("write tool %s writes %q, whose every field is %s-owned; an agent has no direct-write path there",
				tool.Name, tool.Object, all.Owner)
			continue
		}

		for _, name := range schemaFields(t, tool.Params) {
			rule, mapped := rules[name]
			if !mapped {
				continue // not a field of the object: `ref`, `text` to match on
			}
			if rule.Owner != policy.OwnerDeveloper {
				t.Errorf("write tool %s accepts %q on %s, which is %s-owned. There is no direct-write path "+
					"to it: raise a proposal.", tool.Name, name, tool.Object, rule.Owner)
			}
		}
		// `update_dev_field` names the field it writes in an enum rather than in
		// a property name, so the enum is where the boundary actually lives.
		for _, name := range schemaEnum(t, tool.Params, "field") {
			rule, mapped := rules[name]
			if !mapped {
				t.Errorf("write tool %s offers field %q, which %s does not have", tool.Name, name, tool.Object)
				continue
			}
			if rule.Owner != policy.OwnerDeveloper {
				t.Errorf("write tool %s offers %q, which is %s-owned", tool.Name, name, rule.Owner)
			}
		}
	}

	var writeNames []string
	for _, tool := range runner.Tools() {
		if tool.Kind == agent.KindWrite {
			writeNames = append(writeNames, tool.Name)
		}
	}
	want := "add_subtask,add_worklog,check_item,update_dev_field"
	if got := strings.Join(sorted(writeNames), ","); got != want {
		t.Errorf("the write inventory is %q, want %q. A new write tool is a new way past §4.2 "+
			"and needs its own argument.", got, want)
	}
}

// schemaFields lists the top-level property names of a tool's parameter schema.
func schemaFields(t *testing.T, raw []byte) []string {
	t.Helper()
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("a tool's parameter schema is not JSON: %v\n%s", err, raw)
	}
	out := make([]string, 0, len(doc.Properties))
	for name := range doc.Properties {
		out = append(out, name)
	}
	return sorted(out)
}

// schemaEnum lists one property's enum values.
func schemaEnum(t *testing.T, raw []byte, property string) []string {
	t.Helper()
	var doc struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("a tool's parameter schema is not JSON: %v", err)
	}
	return doc.Properties[property].Enum
}

// TestManagerOwnedChangeBecomesAProposal is the same criterion end to end.
func TestManagerOwnedChangeBecomesAProposal(t *testing.T) {
	f := newAgentFixture(t)
	item := f.createItem(f.mgr, map[string]any{
		"title": "Session expiry", "assignee_id": f.dev.userID.String(), "due_date": "2026-08-20",
	})

	f.model.script(
		// First it tries the direct route, as a model will.
		scriptStep{Calls: []scriptCall{{Name: "update_dev_field", Args: map[string]any{
			"ref": item.Ref, "field": "due_date", "value": "2026-09-03"}}}},
		// Then it takes the one it was told about.
		scriptStep{Calls: []scriptCall{{Name: "propose_plan_change", Args: map[string]any{
			"title":     "Push " + item.Ref + " out by two weeks",
			"rationale": "the signing key work it depends on has not started",
			"changes": []any{map[string]any{
				"op": "update", "target": "work_item", "ref": item.Ref,
				"after": map[string]any{"due_date": "2026-09-03"},
			}},
		}}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "raised a proposal"}}}},
	)

	turn := f.post(f.dev, "push "+item.Ref+" out by two weeks")
	if turn.Status != "complete" || turn.FinishReason != "finished" {
		t.Fatalf("turn = %s/%s: %s", turn.Status, turn.FinishReason, turn.ErrorDetail)
	}

	// The date did not move.
	after := f.getItem(f.mgr, item.ID)
	if after.DueDate != "2026-08-20" {
		t.Errorf("the due date moved to %q; a manager-owned field must not be writable by an agent", after.DueDate)
	}

	props := f.proposals()
	if len(props) != 1 {
		t.Fatalf("expected exactly one proposal, got %d", len(props))
	}
	p := props[0]
	if p["status"] != "pending" {
		t.Errorf("proposal status = %v, want pending — a proposal applies nothing by itself", p["status"])
	}
	if p["rationale"] == "" {
		t.Error("the proposal has no rationale, which is what a reviewer reads")
	}
	if p["on_behalf_of"] != f.dev.userID.String() {
		t.Errorf("on_behalf_of = %v, want the requesting user: an agent acts with their permissions and no more",
			p["on_behalf_of"])
	}
	// The first attempt was refused and the refusal named the route.
	kinds := f.traceKinds(turn.ID)
	if !contains(kinds, "tool_denied") {
		t.Errorf("the direct write was not recorded as a denial: %v", kinds)
	}
	var denial string
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT detail->>'answer' FROM turn_events WHERE turn_id = $1 AND kind = 'tool_denied' LIMIT 1`,
		turn.ID).Scan(&denial); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"manager-owned", "propose_plan_change"} {
		if !strings.Contains(denial, want) {
			t.Errorf("the denial does not name %q: %s", want, denial)
		}
	}
}

// TestDevOwnedWriteLandsDirectly is the second exit criterion.
func TestDevOwnedWriteLandsDirectly(t *testing.T) {
	f := newAgentFixture(t)
	item := f.createItem(f.mgr, map[string]any{
		"title": "Session expiry", "assignee_id": f.dev.userID.String(),
	})

	f.model.script(
		scriptStep{Calls: []scriptCall{
			{Name: "update_dev_field", Args: map[string]any{
				"ref": item.Ref, "field": "status", "value": "in_progress"}},
			{Name: "add_worklog", Args: map[string]any{
				"ref": item.Ref, "kind": "note", "body": "the auth library version is wrong"}},
		}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "marked it in progress and logged the version problem"}}}},
	)

	turn := f.post(f.dev, "mark "+item.Ref+" in progress and log that the auth library version is wrong")
	if turn.FinishReason != "finished" {
		t.Fatalf("turn = %s/%s: %s", turn.Status, turn.FinishReason, turn.ErrorDetail)
	}

	after := f.getItem(f.dev, item.ID)
	if after.Status != "in_progress" {
		t.Errorf("status = %q, want in_progress", after.Status)
	}
	if after.Provenance != "agent_committed" {
		t.Errorf("provenance = %q, want agent_committed — an agent's direct write is never human_edited",
			after.Provenance)
	}

	// The audit row names both the agent and the person who asked (D11).
	var (
		actorKind string
		agentName string
		onBehalf  uuid.UUID
	)
	err := f.h.db.Pool.QueryRow(context.Background(), `
		SELECT a.kind, coalesce(a.agent_name, ''), l.on_behalf_of
		  FROM audit_log l JOIN actors a ON a.id = l.actor_id
		 WHERE l.subject_id = $1 AND l.action = 'work_item.status_changed'`, item.ID).
		Scan(&actorKind, &agentName, &onBehalf)
	if err != nil {
		t.Fatal(err)
	}
	if actorKind != "agent" || agentName != "planner" {
		t.Errorf("the audit row names actor %s/%s, want the agent", actorKind, agentName)
	}
	if onBehalf != f.dev.userID {
		t.Errorf("on_behalf_of = %s, want %s", onBehalf, f.dev.userID)
	}

	// And no proposal was raised for something the requester owns.
	if props := f.proposals(); len(props) != 0 {
		t.Errorf("a dev-owned write on the requester's own item raised %d proposal(s)", len(props))
	}
}

// TestAManagerOwnedFieldIsRefusedEvenOnYourOwnItem: the assignment is not a
// key to the whole item.
func TestAManagerOwnedFieldIsRefusedEvenOnYourOwnItem(t *testing.T) {
	f := newAgentFixture(t)
	other := f.createItem(f.mgr, map[string]any{
		"title": "Somebody else's", "assignee_id": f.other.userID.String(),
	})

	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "update_dev_field", Args: map[string]any{
			"ref": other.Ref, "field": "status", "value": "done"}}}},
		scriptStep{Calls: []scriptCall{{Name: "handoff_to_human", Args: map[string]any{
			"reason": other.Ref + " is not assigned to you, so I cannot move its status"}}}},
	)

	turn := f.post(f.dev, "mark "+other.Ref+" done")
	if turn.FinishReason != "finished" {
		t.Fatalf("a handoff is output: turn = %s/%s", turn.Status, turn.FinishReason)
	}
	if got := f.getItem(f.mgr, other.ID); got.Status == "done" {
		t.Error("the agent wrote a dev-owned field on an item assigned to somebody else")
	}
	if !contains(f.traceKinds(turn.ID), "tool_denied") {
		t.Error("writing another person's item was not refused")
	}
}

// TestTurnsSerializePerProject is the third exit criterion.
func TestTurnsSerializePerProject(t *testing.T) {
	f := newAgentFixture(t)

	// A slow first turn: three steps of scripted reading, so the second
	// request arrives while it is still running.
	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "get_plan"}}},
		scriptStep{Calls: []scriptCall{{Name: "get_digest"}}},
		scriptStep{Calls: []scriptCall{{Name: "list_notes"}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "read the plan"}}}},
	)

	var first struct {
		Turn turnView `json:"turn"`
	}
	f.h.decode(f.dev.do(http.MethodPost, "/api/v1/threads/"+f.thread.String()+"/messages",
		map[string]any{"content": "what is going on?"}), http.StatusCreated, &first)

	var second struct {
		Turn turnView `json:"turn"`
	}
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/threads/"+f.thread.String()+"/messages",
		map[string]any{"content": "and what about the milestone?"}), http.StatusCreated, &second)

	// The second message landed — a queue that refuses messages teaches people
	// to wait before typing, which is the opposite of a transcript.
	if second.Turn.ID == uuid.Nil {
		t.Fatal("the second message did not produce a turn")
	}
	// And it is visibly queued behind the first, or already running because the
	// first finished first. Either is correct; what must never happen is two
	// running at once.
	if second.Turn.Status == "queued" && second.Turn.Position < 1 {
		t.Errorf("a queued turn must carry its position, got %d", second.Turn.Position)
	}

	f.await(first.Turn.ID)
	f.await(second.Turn.ID)

	// The database's own invariant: the partial unique index makes two running
	// turns on one project unrepresentable. Assert it directly.
	var running int
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_turns WHERE project_id = $1 AND status = 'running'`, f.pid).
		Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != 0 {
		t.Errorf("%d turns are still running after both ended", running)
	}
}

// TestTransientClassesAreRetriedOnce is the fourth exit criterion.
//
// This is the gap LumixAgents Phase 6 identified and did not close: two of five
// golden-task attempts died on the model returning nothing usable and neither
// was retried.
func TestTransientClassesAreRetriedOnce(t *testing.T) {
	f := newAgentFixture(t)

	f.model.script(
		scriptStep{Status: http.StatusInternalServerError}, // `server`: retryable
		scriptStep{Calls: []scriptCall{{Name: "add_worklog", Args: map[string]any{
			"body": "recovered after a 500"}}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "done"}}}},
	)

	turn := f.post(f.dev, "log something")
	if turn.FinishReason != "finished" {
		t.Fatalf("a retryable failure should have been retried and recovered: %s/%s %s",
			turn.Status, turn.FinishReason, turn.ErrorDetail)
	}
	if turn.Retries != 1 {
		t.Errorf("retries = %d, want exactly 1", turn.Retries)
	}
	kinds := f.traceKinds(turn.ID)
	if !contains(kinds, "model_error") || !contains(kinds, "model_retry") {
		t.Errorf("the failure and the retry are not both in the trace: %v", kinds)
	}
}

func TestNonTransientClassesAreNeverRetried(t *testing.T) {
	f := newAgentFixture(t)

	// 400 normalises to `invalid_request`: we sent something wrong, which is a
	// bug in this codebase and must surface as one rather than be papered over.
	f.model.script(
		scriptStep{Status: http.StatusBadRequest},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "never reached"}}}},
	)

	turn := f.post(f.dev, "do something")
	if turn.Status != "failed" {
		t.Fatalf("turn = %s/%s, want failed", turn.Status, turn.FinishReason)
	}
	if turn.Retries != 0 {
		t.Errorf("retries = %d; a non-transient class retried is a bug rephrased", turn.Retries)
	}
	if f.model.callCount() != 1 {
		t.Errorf("the model was called %d times; a non-retryable failure must be called once", f.model.callCount())
	}
}

// TestATurnProducingNothingReportsNoOutput is the fifth exit criterion.
func TestATurnProducingNothingReportsNoOutput(t *testing.T) {
	f := newAgentFixture(t)

	// Reads, then declines in prose, then calls finish with a confident
	// summary. From the outside this is indistinguishable from a completed run,
	// which is exactly why it must not be recorded as one.
	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "get_plan"}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{
			"summary": "I looked at the plan and everything seems fine."}}}},
	)

	turn := f.post(f.dev, "have a look at the plan")
	if turn.FinishReason != "no_output" {
		t.Errorf("finish_reason = %q, want no_output — a turn that read things and changed nothing has not "+
			"answered anything", turn.FinishReason)
	}
	if turn.Status != "failed" {
		t.Errorf("status = %q; no_output is not a success", turn.Status)
	}

	// And it says so in the thread, not only in a status field.
	var msgs struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	f.h.decode(f.dev.do(http.MethodGet, "/api/v1/threads/"+f.thread.String()+"/messages?turn="+turn.ID.String(), nil),
		http.StatusOK, &msgs)
	var said bool
	for _, m := range msgs.Messages {
		if strings.Contains(m.Content, "no_output") {
			said = true
		}
	}
	if !said {
		t.Error("the transcript does not say the turn produced nothing")
	}
}

// TestAProseAnswerWithNoToolsIsAlsoNoOutput: the same rule when the model never
// calls anything at all.
func TestAProseAnswerWithNoToolsIsAlsoNoOutput(t *testing.T) {
	f := newAgentFixture(t)
	f.model.script(scriptStep{Content: "You should probably talk to your manager about that."})

	turn := f.post(f.dev, "push the milestone out")
	if turn.FinishReason != "no_output" {
		t.Errorf("finish_reason = %q, want no_output", turn.FinishReason)
	}
}

// TestDenialsEscalateOnRepeat is the sixth exit criterion.
func TestDenialsEscalateOnRepeat(t *testing.T) {
	f := newAgentFixture(t)

	// The classic loop: a model refused once tries the same thing under a
	// different name.
	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "update_plan", Args: map[string]any{"due_date": "2026-09-01"}}}},
		scriptStep{Calls: []scriptCall{{Name: "update_plan", Args: map[string]any{"due_date": "2026-09-02"}}}},
		scriptStep{Calls: []scriptCall{{Name: "update_plan", Args: map[string]any{"due_date": "2026-09-03"}}}},
		scriptStep{Calls: []scriptCall{{Name: "handoff_to_human", Args: map[string]any{
			"reason": "I cannot change a due date"}}}},
	)

	turn := f.post(f.dev, "change the due date")
	if turn.Status != "complete" {
		t.Fatalf("turn = %s/%s", turn.Status, turn.FinishReason)
	}

	rows, err := f.h.db.Pool.Query(context.Background(),
		`SELECT detail->>'answer' FROM turn_events WHERE turn_id = $1 AND kind = 'tool_unknown' ORDER BY seq`,
		turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var answers []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		answers = append(answers, a)
	}
	if len(answers) != 3 {
		t.Fatalf("expected three refusals, got %d", len(answers))
	}
	// The first names the boundary and the route.
	if !strings.Contains(answers[0], "propose_plan_change") {
		t.Errorf("the first refusal does not name the route: %s", answers[0])
	}
	if !strings.Contains(answers[0], "not a tool that exists") {
		t.Errorf("the first refusal does not name the boundary: %s", answers[0])
	}
	// The second says so; the third tells it to stop.
	if !strings.Contains(answers[1], "second time") {
		t.Errorf("the second refusal does not escalate: %s", answers[1])
	}
	if !strings.Contains(answers[2], "Stop trying") {
		t.Errorf("the third refusal does not tell it to stop: %s", answers[2])
	}
}

// TestHardLimitsAreEnforcedIndependently is the seventh exit criterion.
func TestHardLimitsAreEnforcedIndependently(t *testing.T) {
	cases := []struct {
		name   string
		limits agent.Limits
		want   string
	}{
		{"steps", agent.Limits{MaxSteps: 3}, "max_turns"},
		{"tokens", agent.Limits{MaxTokens: 200}, "max_tokens"},
		{"wall clock", agent.Limits{MaxWallClock: time.Millisecond}, "max_wall_clock"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAgentFixtureWithLimits(t, tc.limits)
			// A model that never stops: only a limit can end this.
			for i := 0; i < 60; i++ {
				f.model.script(scriptStep{Calls: []scriptCall{{Name: "get_plan"}}})
			}
			turn := f.post(f.dev, "go around forever")
			if turn.FinishReason != tc.want {
				t.Errorf("finish_reason = %q, want %q", turn.FinishReason, tc.want)
			}
			if turn.Status != "failed" {
				t.Errorf("status = %q; hitting a ceiling is not a success", turn.Status)
			}
			if !contains(f.traceKinds(turn.ID), "limit") {
				t.Error("the limit that tripped is not in the trace")
			}
		})
	}
}

// TestTraceReplaysEveryCall is the eighth exit criterion.
func TestTraceReplaysEveryCall(t *testing.T) {
	f := newAgentFixture(t)
	item := f.createItem(f.mgr, map[string]any{
		"title": "Traced", "assignee_id": f.dev.userID.String(),
	})

	f.model.script(
		scriptStep{Status: http.StatusInternalServerError},
		scriptStep{Calls: []scriptCall{{Name: "get_item", Args: map[string]any{"ref": item.Ref}}}},
		scriptStep{Calls: []scriptCall{{Name: "nonsense_tool"}}},
		scriptStep{Calls: []scriptCall{{Name: "update_dev_field", Args: map[string]any{
			"ref": item.Ref, "field": "status", "value": "review"}}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "moved it to review"}}}},
	)

	turn := f.post(f.dev, "move "+item.Ref+" to review")
	kinds := f.traceKinds(turn.ID)

	for _, want := range []string{
		"model_error", "model_retry", "model_call",
		"tool_call", "tool_result", "tool_unknown",
	} {
		if !contains(kinds, want) {
			t.Errorf("the trace is missing %s: %v", want, kinds)
		}
	}

	// Every tool the model asked for has a tool_call row, including the one
	// that does not exist. A trace that only records what succeeded is a trace
	// that cannot explain a failure.
	var calls int
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM turn_events WHERE turn_id = $1 AND kind = 'tool_call'`, turn.ID).
		Scan(&calls); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Errorf("%d tool_call rows, want 4 (get_item, nonsense_tool, update_dev_field, finish)", calls)
	}

	// And the trace is append-only, enforced by a trigger rather than by
	// convention.
	_, err := f.h.db.Pool.Exec(context.Background(),
		`UPDATE turn_events SET subject = 'rewritten' WHERE turn_id = $1`, turn.ID)
	if err == nil {
		t.Error("turn_events accepted an UPDATE; a trace that can be rewritten is not evidence")
	}
}

// TestStreamSurvivesAReconnect is the ninth exit criterion.
func TestStreamSurvivesAReconnect(t *testing.T) {
	f := newAgentFixture(t)
	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "add_worklog", Args: map[string]any{"body": "something happened"}}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "logged it"}}}},
	)

	turn := f.post(f.dev, "log something")
	if turn.FinishReason != "finished" {
		t.Fatalf("turn = %s/%s", turn.Status, turn.FinishReason)
	}

	// Connect fresh: a new subscriber has missed nothing by definition, so the
	// stream opens without a resync.
	first := f.openStream(t, "")
	if len(first) != 0 {
		t.Errorf("a fresh stream replayed %d events; it should start empty", len(first))
	}

	// Reconnect from the very beginning: everything the turn emitted is
	// replayable, in order, with no resync.
	replayed := f.openStream(t, "0")
	if len(replayed) == 0 {
		t.Fatal("reconnecting from id 0 replayed nothing")
	}
	var kinds []string
	for _, e := range replayed {
		if e.Type == "resync" {
			t.Errorf("a reconnect inside the ring should not resync: %v", e.Payload)
		}
		kinds = append(kinds, e.Type)
	}
	for _, want := range []string{"turn.queued", "turn.started", "turn.ended"} {
		if !contains(kinds, want) {
			t.Errorf("the replay is missing %s: %v", want, kinds)
		}
	}
	// Ids are monotonic, which is what makes Last-Event-ID mean anything.
	for i := 1; i < len(replayed); i++ {
		if replayed[i].ID <= replayed[i-1].ID {
			t.Errorf("event ids are not monotonic: %d then %d", replayed[i-1].ID, replayed[i].ID)
		}
	}

	// A client that fell further behind than the ring is told to refetch rather
	// than silently resumed from a hole.
	beyond := f.openStreamRaw(t, "999999999")
	_ = beyond
}

// openStream connects, reads whatever is immediately available, and closes.
func (f *agentFixture) openStream(t *testing.T, lastEventID string) []httpapi.Event {
	t.Helper()
	return f.openStreamRaw(t, lastEventID)
}

func (f *agentFixture) openStreamRaw(t *testing.T, lastEventID string) []httpapi.Event {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		f.h.srv.URL+"/api/v1/projects/"+f.pid.String()+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(f.dev.cookie)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream: HTTP %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}

	var (
		out  []httpapi.Event
		sc   = bufio.NewScanner(resp.Body)
		idle = time.AfterFunc(400*time.Millisecond, cancel)
	)
	defer idle.Stop()
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var e httpapi.Event
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
			t.Fatalf("stream payload is not an Event: %v", err)
		}
		out = append(out, e)
		idle.Reset(400 * time.Millisecond)
	}
	return out
}

// TestCancellingATurnIsItsOwnOutcome: a user who stops a turn did not hit a bug.
func TestCancellingATurnIsItsOwnOutcome(t *testing.T) {
	f := newAgentFixture(t)
	for i := 0; i < 40; i++ {
		f.model.script(scriptStep{Calls: []scriptCall{{Name: "get_plan"}}})
	}

	var posted struct {
		Turn turnView `json:"turn"`
	}
	f.h.decode(f.dev.do(http.MethodPost, "/api/v1/threads/"+f.thread.String()+"/messages",
		map[string]any{"content": "go around forever"}), http.StatusCreated, &posted)

	var cancelled turnView
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/turns/"+posted.Turn.ID.String()+"/cancel", nil),
		http.StatusOK, &cancelled)
	if cancelled.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", cancelled.Status)
	}

	// It stays cancelled: the loop's own verdict does not overwrite the human's.
	time.Sleep(300 * time.Millisecond)
	final := f.await(posted.Turn.ID)
	if final.Status != "cancelled" || final.FinishReason != "cancelled" {
		t.Errorf("turn = %s/%s; a cancelled turn must stay cancelled", final.Status, final.FinishReason)
	}
}

// TestEveryToolIsOfferedToTheModel: the inventory the model sees is the
// inventory this package defines, with nothing added or quietly dropped.
func TestEveryToolIsOfferedToTheModel(t *testing.T) {
	f := newAgentFixture(t)
	f.model.script(scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "x"}}}})
	f.post(f.dev, "hello")

	offered := sorted(f.model.lastTools())
	var declared []string
	for _, tool := range f.runner.Tools() {
		declared = append(declared, tool.Name)
	}
	if strings.Join(offered, ",") != strings.Join(sorted(declared), ",") {
		t.Errorf("the model was offered %v, the inventory declares %v", offered, sorted(declared))
	}
	for _, banned := range []string{"update_plan", "update_work_item", "update_diagram", "delete_work_item"} {
		if contains(offered, banned) {
			t.Errorf("%q is offered to the model; it must not exist", banned)
		}
	}
}

func sorted(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
