package httpapi_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/contextsvc"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/governance"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// Phase 7's exit criteria, over the real HTTP surface and a real Postgres.
//
// The proposals here are raised by the agent through its actual propose tools
// rather than inserted into the table, because half of what Phase 7 has to get
// right is the `before` snapshot those tools record — and a fixture that writes
// its own proposals would be testing the applier against a premise no tool
// produces.

type govFixture struct {
	*agentFixture
}

func newGovFixture(t *testing.T) *govFixture {
	t.Helper()
	return &govFixture{agentFixture: newAgentFixture(t)}
}

type proposalView struct {
	ID                uuid.UUID `json:"id"`
	Title             string    `json:"title"`
	Rationale         string    `json:"rationale"`
	TargetKind        string    `json:"target_kind"`
	Status            string    `json:"status"`
	DecisionRationale string    `json:"decision_rationale"`
	CanApprove        bool      `json:"can_approve"`
	Blocked           string    `json:"approval_blocked_because"`

	Changes []struct {
		Seq      int            `json:"seq"`
		Op       string         `json:"op"`
		Target   string         `json:"target"`
		TargetID string         `json:"target_id"`
		Before   map[string]any `json:"before"`
		After    map[string]any `json:"after"`
	} `json:"changes"`

	Conflicts []struct {
		Seq      int    `json:"seq"`
		Element  string `json:"element"`
		Field    string `json:"field"`
		Expected string `json:"expected"`
		Actual   string `json:"actual"`
		Reason   string `json:"reason"`
	} `json:"conflicts"`
}

func (f *govFixture) proposal(c *client, id uuid.UUID) proposalView {
	f.h.t.Helper()
	var out proposalView
	f.h.decode(c.do(http.MethodGet, "/api/v1/proposals/"+id.String(), nil), http.StatusOK, &out)
	return out
}

// onlyProposal fails unless exactly one proposal exists, which is what every
// test here wants and what a silent second one would quietly break.
func (f *govFixture) onlyProposal() uuid.UUID {
	f.h.t.Helper()
	props := f.proposals()
	if len(props) != 1 {
		f.h.t.Fatalf("expected exactly one proposal, got %d", len(props))
	}
	id, err := uuid.Parse(props[0]["id"].(string))
	if err != nil {
		f.h.t.Fatal(err)
	}
	return id
}

// proposePlan runs a turn whose only act is to raise the given plan changes.
func (f *govFixture) proposePlan(title, rationale string, changes ...map[string]any) uuid.UUID {
	f.h.t.Helper()
	raw := make([]any, 0, len(changes))
	for _, c := range changes {
		raw = append(raw, c)
	}
	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "propose_plan_change", Args: map[string]any{
			"title": title, "rationale": rationale, "changes": raw,
		}}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "raised a proposal"}}}},
	)
	turn := f.post(f.dev, title)
	if turn.FinishReason != "finished" {
		f.h.t.Fatalf("the proposing turn ended as %s/%s: %s", turn.Status, turn.FinishReason, turn.ErrorDetail)
	}
	return f.onlyProposal()
}

func (f *govFixture) approve(c *client, id uuid.UUID, want int) map[string]any {
	f.h.t.Helper()
	var body map[string]any
	f.h.decode(c.do(http.MethodPost, "/api/v1/proposals/"+id.String()+"/approve", nil), want, &body)
	return body
}

// ── exit criterion 1: atomic or nothing ──────────────────────────────────────

// TestProposalAppliesAtomicallyAsOneRevision covers the happy half.
func TestProposalAppliesAtomicallyAsOneRevision(t *testing.T) {
	f := newGovFixture(t)
	one := f.createItem(f.mgr, map[string]any{
		"title": "Session expiry", "assignee_id": f.dev.userID.String(), "due_date": "2026-08-20",
	})
	two := f.createItem(f.mgr, map[string]any{"title": "Token rotation", "priority": "low"})

	id := f.proposePlan("Re-sequence the auth work",
		"the signing key work has not started, so both dates are wrong and the priorities are the wrong way round",
		map[string]any{"op": "update", "target": "work_item", "ref": one.Ref,
			"after": map[string]any{"due_date": "2026-09-03", "priority": "urgent"}},
		map[string]any{"op": "update", "target": "work_item", "ref": two.Ref,
			"after": map[string]any{"priority": "high", "title": "Signing key rotation"}},
	)

	f.approve(f.mgr, id, http.StatusOK)

	after := f.getItem(f.mgr, one.ID)
	if after.DueDate != "2026-09-03" || after.Priority != "urgent" {
		t.Errorf("change 1 did not land: due=%q priority=%q", after.DueDate, after.Priority)
	}
	other := f.getItem(f.mgr, two.ID)
	if other.Priority != "high" || other.Title != "Signing key rotation" {
		t.Errorf("change 2 did not land: priority=%q title=%q", other.Priority, other.Title)
	}

	got := f.proposal(f.mgr, id)
	if got.Status != "approved" {
		t.Errorf("status = %q, want approved", got.Status)
	}

	// The audit trail says both that it was decided and that it was applied.
	// Two facts: a proposal can be decided and then fail, and the export has to
	// be able to tell those apart.
	for _, action := range []string{"proposal.decided", "proposal.applied"} {
		var n int
		if err := f.h.db.Pool.QueryRow(context.Background(),
			`SELECT count(*) FROM audit_log WHERE subject_id = $1 AND action = $2`, id, action).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s written %d times, want once", action, n)
		}
	}
}

// TestAFailingChangeAppliesNothing is the half that matters.
//
// A proposal whose second change cannot land must leave the first one unwritten.
// The failure is one the propose-time check cannot catch, and the dependency
// cycle here is the honest example of that: every ref in it names a real item,
// nothing about the change is malformed, and whether it closes a loop is a fact
// about the whole graph at the moment of approval rather than about the change.
// This is exactly the case where a half-applied proposal would otherwise be
// discovered by a person wondering why one of the two things they agreed to
// happened.
func TestAFailingChangeAppliesNothing(t *testing.T) {
	f := newGovFixture(t)
	one := f.createItem(f.mgr, map[string]any{"title": "Session expiry", "priority": "low"})
	two := f.createItem(f.mgr, map[string]any{"title": "Token rotation"})

	// two already blocks one, so the proposal's second change closes the ring.
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+two.ID.String()+"/dependencies",
		map[string]any{"blocks": []string{one.ID.String()}}), http.StatusOK, nil)

	id := f.proposePlan("Two changes, one of them impossible", "exercising atomicity",
		map[string]any{"op": "update", "target": "work_item", "ref": one.Ref,
			"after": map[string]any{"priority": "urgent"}},
		map[string]any{"op": "update", "target": "work_item", "ref": one.Ref,
			"after": map[string]any{"dependencies": map[string]any{"blocks": []any{two.Ref}}}},
	)

	body := f.approve(f.mgr, id, http.StatusBadRequest)
	if msg := errMessage(body); !strings.Contains(msg, two.Ref) {
		t.Errorf("the failure does not name the items that caused it: %s", msg)
	}

	if got := f.getItem(f.mgr, one.ID).Priority; got != "low" {
		t.Errorf("change 1 landed anyway (priority = %q). A proposal is one revision or none.", got)
	}
	if got := f.proposal(f.mgr, id).Status; got != "pending" {
		t.Errorf("status = %q, want pending: a proposal that could not be applied was not decided", got)
	}
}

// ── exit criterion 2: stale proposals are superseded, with the conflict named ─

func TestStaleProposalIsSupersededWithTheConflictNamed(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{
		"title": "Session expiry", "assignee_id": f.dev.userID.String(), "due_date": "2026-08-20",
	})

	id := f.proposePlan("Push "+item.Ref+" out by two weeks",
		"the signing key work it depends on has not started",
		map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
			"after": map[string]any{"due_date": "2026-09-03"}},
	)

	// The world moves under the proposal.
	f.h.decode(f.patchItem(f.mgr, item.ID, map[string]any{"due_date": "2026-08-27"}), http.StatusOK, nil)

	// The read says so before anybody spends attention on it.
	pending := f.proposal(f.mgr, id)
	if len(pending.Conflicts) != 1 {
		t.Fatalf("the read reported %d conflicts, want 1: a reviewer must see this before deciding", len(pending.Conflicts))
	}
	c := pending.Conflicts[0]
	if c.Field != "due_date" || c.Expected != "2026-08-20" || c.Actual != "2026-08-27" {
		t.Errorf("conflict = %+v, want due_date 2026-08-20 → 2026-08-27", c)
	}

	body := f.approve(f.mgr, id, http.StatusConflict)
	msg := errMessage(body)
	for _, want := range []string{"superseded", "due_date", "2026-08-20", "2026-08-27"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the conflict message does not name %q:\n%s", want, msg)
		}
	}

	// Nothing was applied and nothing was merged.
	if got := f.getItem(f.mgr, item.ID).DueDate; got != "2026-08-27" {
		t.Errorf("due_date = %q; the approval overwrote a change made after the proposal was raised", got)
	}
	after := f.proposal(f.mgr, id)
	if after.Status != "superseded" {
		t.Errorf("status = %q, want superseded", after.Status)
	}
	if !strings.Contains(after.DecisionRationale, "due_date") {
		t.Errorf("the decision rationale does not record what moved: %q", after.DecisionRationale)
	}
}

// TestAnUnrelatedEditDoesNotSupersede pins the narrow reading of "the target
// moved" (D57).
//
// The propose tools record a fixed snapshot whatever the change is about, so the
// broad reading — any recorded field moving is a conflict — means a developer
// ticking a status supersedes every pending proposal about that item. A conflict
// that fires constantly is one people learn to click past, which costs more than
// the case it guards.
func TestAnUnrelatedEditDoesNotSupersede(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{
		"title": "Session expiry", "assignee_id": f.dev.userID.String(),
		"due_date": "2026-08-20", "priority": "low",
	})

	id := f.proposePlan("Raise the priority", "it is on the critical path now",
		map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
			"after": map[string]any{"priority": "urgent"}},
	)

	// The assignee reports reality on a field the proposal says nothing about.
	f.h.decode(f.patchItem(f.dev, item.ID, map[string]any{"status": "in_progress"}), http.StatusOK, nil)

	if c := f.proposal(f.mgr, id).Conflicts; len(c) != 0 {
		t.Errorf("a status report superseded a priority proposal: %+v", c)
	}
	f.approve(f.mgr, id, http.StatusOK)

	after := f.getItem(f.mgr, item.ID)
	if after.Priority != "urgent" {
		t.Errorf("priority = %q, want urgent", after.Priority)
	}
	if after.Status != "in_progress" {
		t.Errorf("status = %q; approving the proposal reverted the developer's report", after.Status)
	}
}

// TestAProposalAboutADeletedItemIsAConflictNotACrash: the target going away is
// the same class of staleness as a field moving, and reads as one.
func TestAProposalAboutADeletedItemIsAConflictNotACrash(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry", "due_date": "2026-08-20"})

	id := f.proposePlan("Push it out", "it is not going to make the date",
		map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
			"after": map[string]any{"due_date": "2026-09-03"}},
	)
	f.h.decode(f.mgr.do(http.MethodDelete, "/api/v1/work-items/"+item.ID.String(), nil), http.StatusNoContent, nil)

	body := f.approve(f.mgr, id, http.StatusConflict)
	if msg := errMessage(body); !strings.Contains(msg, "deleted") {
		t.Errorf("the conflict does not say the item was deleted: %s", msg)
	}
	if got := f.proposal(f.mgr, id).Status; got != "superseded" {
		t.Errorf("status = %q, want superseded", got)
	}
}

// ── exit criterion 3: rejection needs a rationale, and it reaches context ─────

func TestRejectionWithoutARationaleIsRefused(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry", "due_date": "2026-08-20"})
	id := f.proposePlan("Push it out", "it is not going to make the date",
		map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
			"after": map[string]any{"due_date": "2026-09-03"}},
	)

	for _, body := range []map[string]any{{}, {"rationale": ""}, {"rationale": "no"}} {
		resp := f.mgr.do(http.MethodPost, "/api/v1/proposals/"+id.String()+"/reject", body)
		if resp.StatusCode != http.StatusUnprocessableEntity && resp.StatusCode != http.StatusBadRequest {
			t.Errorf("rejecting with %v gave %d, want a validation failure — a refusal with no reason "+
				"reads downstream as though nobody looked", body, resp.StatusCode)
		}
		resp.Body.Close()
	}

	if got := f.proposal(f.mgr, id).Status; got != "pending" {
		t.Errorf("status = %q after three refused rejections, want pending", got)
	}
}

// TestRejectionRationaleReachesAssembledContext is the whole point of §5.2.
func TestRejectionRationaleReachesAssembledContext(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry", "due_date": "2026-08-20"})
	id := f.proposePlan("Push "+item.Ref+" out by two weeks",
		"the signing key work it depends on has not started",
		map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
			"after": map[string]any{"due_date": "2026-09-03"}},
	)

	const reason = "the date is contractual; the queue is deliberate and the signing key work is being dropped"
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/proposals/"+id.String()+"/reject",
		map[string]any{"rationale": reason}), http.StatusOK, nil)

	if got := f.getItem(f.mgr, item.ID).DueDate; got != "2026-08-20" {
		t.Errorf("due_date = %q; a rejection changed something", got)
	}

	text := f.assembledContext(t)
	if !strings.Contains(text, "## PREVIOUSLY REJECTED") {
		t.Fatalf("assembled context has no rejection section:\n\n%s", text)
	}
	rejected := text[strings.Index(text, "## PREVIOUSLY REJECTED"):]
	for _, want := range []string{"Push " + item.Ref + " out", "REJECTED because:", "contractual"} {
		if !strings.Contains(rejected, want) {
			t.Errorf("the rejection section is missing %q:\n\n%s", want, rejected)
		}
	}
}

// assembledContext builds the context a turn would receive, so a test can
// assert on the bytes the model actually reads.
func (f *govFixture) assembledContext(t *testing.T) string {
	t.Helper()
	svc := contextsvc.New(f.h.db, slog.New(slog.DiscardHandler), nil)
	ctx := telemetry.WithRequestID(context.Background(), telemetry.NewRequestID())
	actor, err := repo.Actors{}.ForUser(ctx, f.h.db.Pool, f.admin.userID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Assemble(ctx, f.pid, contextsvc.Request{Message: "what should I do next?"},
		policy.Principal{UserID: f.admin.userID, ActorID: actor.ID, GlobalRole: policy.RoleAdmin, IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	return res.Text
}

// ── exit criterion 5: developer approval is limited to what they own ─────────

func TestDeveloperApprovalIsLimitedToDevOwnedProposals(t *testing.T) {
	f := newGovFixture(t)
	mine := f.createItem(f.mgr, map[string]any{
		"title": "Session expiry", "assignee_id": f.dev.userID.String(), "due_date": "2026-08-20",
	})

	// A manager-owned field. The developer is the assignee and still may not
	// decide it — the assignment is not a key to the whole item.
	dates := f.proposePlan("Push "+mine.Ref+" out", "the signing key work has not started",
		map[string]any{"op": "update", "target": "work_item", "ref": mine.Ref,
			"after": map[string]any{"due_date": "2026-09-03"}},
	)

	view := f.proposal(f.dev, dates)
	if view.CanApprove {
		t.Error("the read says a developer can approve a due-date change")
	}
	if !strings.Contains(view.Blocked, "manager") {
		t.Errorf("the reason does not name who can: %q", view.Blocked)
	}

	body := f.approve(f.dev, dates, http.StatusForbidden)
	msg := errMessage(body)
	for _, want := range []string{"manager", "due_date"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the denial does not mention %q: %s", want, msg)
		}
	}
	if got := f.getItem(f.mgr, mine.ID).DueDate; got != "2026-08-20" {
		t.Errorf("due_date = %q after a refused approval", got)
	}
	// A manager can.
	f.approve(f.mgr, dates, http.StatusOK)
	if got := f.getItem(f.mgr, mine.ID).DueDate; got != "2026-09-03" {
		t.Errorf("due_date = %q after the manager approved", got)
	}
}

// TestDeveloperMayApproveAWhollyDevOwnedProposal is the other side of the rule:
// "limited to" is not "never".
func TestDeveloperMayApproveAWhollyDevOwnedProposal(t *testing.T) {
	f := newGovFixture(t)
	mine := f.createItem(f.mgr, map[string]any{
		"title": "Session expiry", "assignee_id": f.dev.userID.String(),
	})

	id := f.proposePlan("Mark "+mine.Ref+" blocked", "the vendor library does not do range queries",
		map[string]any{"op": "update", "target": "work_item", "ref": mine.Ref,
			"after": map[string]any{"status": "blocked"}},
	)

	if view := f.proposal(f.dev, id); !view.CanApprove {
		t.Errorf("the assignee cannot approve a status change on their own item: %q", view.Blocked)
	}
	f.approve(f.dev, id, http.StatusOK)
	if got := f.getItem(f.dev, mine.ID).Status; got != "blocked" {
		t.Errorf("status = %q, want blocked", got)
	}
}

// TestADeveloperCannotApproveSomebodyElsesDevOwnedField: "their own" is the
// whole content of the dev-owned scope, and a proposal is not a way around it.
func TestADeveloperCannotApproveSomebodyElsesDevOwnedField(t *testing.T) {
	f := newGovFixture(t)
	theirs := f.createItem(f.mgr, map[string]any{
		"title": "Token rotation", "assignee_id": f.other.userID.String(),
	})

	id := f.proposePlan("Mark "+theirs.Ref+" blocked", "it is waiting on the same library",
		map[string]any{"op": "update", "target": "work_item", "ref": theirs.Ref,
			"after": map[string]any{"status": "blocked"}},
	)

	f.approve(f.dev, id, http.StatusForbidden)
	if got := f.getItem(f.mgr, theirs.ID).Status; got != "todo" {
		t.Errorf("status = %q; a developer approved a change to somebody else's item", got)
	}
}

// ── exit criterion 4: re-evaluate returns a proposal and sees provenance ─────

// diagram stands a small graph up inside the governance fixture and returns its
// id and slug.
func (f *govFixture) diagram(t *testing.T) (uuid.UUID, string) {
	t.Helper()
	var created struct {
		ID   uuid.UUID `json:"id"`
		Slug string    `json:"slug"`
	}
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/diagrams",
		map[string]any{"name": "Auth Context", "level": "context"}), http.StatusCreated, &created)

	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+created.ID.String()+"/lease", nil),
		http.StatusOK, nil)
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+created.ID.String()+"/revisions", map[string]any{
		"base_revision": 0, "summary": "initial auth context",
		"nodes": []map[string]any{
			{"slug": "auth-service", "kind": "service", "name": "Auth Service"},
			{"slug": "token-store", "kind": "datastore", "name": "Token Store"},
		},
		"edges": []map[string]any{
			{"from": "auth-service", "to": "token-store", "relation": "writes_to"},
		},
	}), http.StatusCreated, nil)
	return created.ID, created.Slug
}

// asExtracted rewrites one element's provenance to what an ingest would have
// left, so the fixture has both kinds without needing a photograph.
//
// **Both rows.** A node is a placement and its kind and name live on the entity,
// so an ingest writes `vlm_extracted` to each — and marking only the placement
// would leave the entity saying a human created it, which is exactly what
// EffectiveProvenance now reads.
func (f *govFixture) asExtracted(t *testing.T, diagramID uuid.UUID, slug string) {
	t.Helper()
	tag, err := f.h.db.Pool.Exec(context.Background(), `
		UPDATE diagram_nodes n SET provenance = 'vlm_extracted'
		  FROM catalog_entities e
		 WHERE n.entity_id = e.id AND n.diagram_id = $1 AND e.slug = $2`, diagramID, slug)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("expected to mark one node, marked %d", tag.RowsAffected())
	}
	if _, err := f.h.db.Pool.Exec(context.Background(),
		`UPDATE catalog_entities SET provenance = 'vlm_extracted' WHERE project_id = $1 AND slug = $2`,
		f.pid, slug); err != nil {
		t.Fatal(err)
	}
}

// TestReevaluateReturnsAProposalAndRespectsProvenance is the fourth exit
// criterion, and the one the whole `provenance` field exists to pay for.
func TestReevaluateReturnsAProposalAndRespectsProvenance(t *testing.T) {
	f := newGovFixture(t)
	did, _ := f.diagram(t)
	f.asExtracted(t, did, "token-store")

	// The model wants to change both: the one a human set, and the one it read
	// off the page itself. Only the second may become a change.
	f.model.script(scriptStep{Content: `{"corrections":[
	  {"action":"update_node","slug":"auth-service","kind":"component",
	   "reason":"it looks like a component of the gateway on the page"},
	  {"action":"update_node","slug":"token-store","kind":"queue",
	   "reason":"the box is drawn as a queue, not a store"},
	  {"action":"add_edge","from":"token-store","to":"auth-service","relation":"publishes_to",
	   "reason":"there is an arrow back that was missed"}
	]}`})

	var out struct {
		Proposal  *proposalView `json:"proposal"`
		PagesRead int           `json:"pages_read"`
		Message   string        `json:"message"`
	}
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+did.String()+"/reevaluate", nil),
		http.StatusOK, &out)

	if out.Proposal == nil {
		t.Fatalf("re-evaluate produced no proposal: %s", out.Message)
	}
	if out.Proposal.Status != "pending" {
		t.Errorf("status = %q — re-evaluate must return a proposal, never a write", out.Proposal.Status)
	}
	if out.Proposal.TargetKind != "diagram" {
		t.Errorf("target_kind = %q, want diagram", out.Proposal.TargetKind)
	}

	// Nothing moved on the canvas.
	var kind string
	if err := f.h.db.Pool.QueryRow(context.Background(), `
		SELECT e.kind FROM diagram_nodes n JOIN catalog_entities e ON e.id = n.entity_id
		 WHERE n.diagram_id = $1 AND e.slug = 'auth-service'`, did).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "service" {
		t.Errorf("auth-service is now %q; re-evaluate wrote to the diagram", kind)
	}

	// The human-set node was refused, and the refusal is in the rationale rather
	// than swallowed: "we re-read it" must never quietly mean "and threw part of
	// the answer away".
	var slugs []string
	for _, c := range out.Proposal.Changes {
		slugs = append(slugs, strings.TrimSpace(mapString(c.After, "slug")+mapString(c.After, "from")))
	}
	for _, unwanted := range slugs {
		if unwanted == "auth-service" {
			t.Error("a correction to a human-set node became a change; the whole flow turns on not doing that")
		}
	}
	if !strings.Contains(out.Proposal.Rationale, "auth-service") {
		t.Errorf("the dropped correction is not reported:\n%s", out.Proposal.Rationale)
	}
	if !strings.Contains(out.Proposal.Rationale, "human") {
		t.Errorf("the rationale does not say why it was dropped:\n%s", out.Proposal.Rationale)
	}

	// And the extracted node's correction survived.
	found := false
	for _, c := range out.Proposal.Changes {
		if mapString(c.After, "slug") == "token-store" && mapString(c.After, "kind") == "queue" {
			found = true
		}
	}
	if !found {
		t.Errorf("the correction to the extracted node did not survive: %+v", out.Proposal.Changes)
	}

	// The model was actually shown the provenance. This is the input §5.4 calls
	// the highest-signal one it will get, and a prompt that quietly stopped
	// carrying it would leave every assertion above still passing.
	prompt := f.lastSystemPrompt(t)
	for _, want := range []string{"provenance=human_created", "provenance=vlm_extracted", "decisions"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the re-evaluation prompt does not carry %q", want)
		}
	}
}

// TestADiagramProposalLandsAsOneRevision: several graph edits to one picture
// produce one revision, not one each.
func TestADiagramProposalLandsAsOneRevision(t *testing.T) {
	f := newGovFixture(t)
	did, slug := f.diagram(t)

	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "propose_diagram_change", Args: map[string]any{
			"title":     "Put a cache in front of the token store",
			"rationale": "the read path is hot and the store is the bottleneck",
			"diagram":   slug,
			"changes": []any{
				map[string]any{"op": "create", "target": "diagram_node", "after": map[string]any{
					"slug": "session-cache", "kind": "datastore", "name": "Session Cache"}},
				map[string]any{"op": "create", "target": "diagram_edge", "after": map[string]any{
					"from": "auth-service", "to": "session-cache", "relation": "reads_from"}},
				map[string]any{"op": "delete", "target": "diagram_edge", "after": map[string]any{
					"from": "auth-service", "to": "token-store", "relation": "writes_to"}},
			},
		}}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "proposed a cache"}}}},
	)
	turn := f.post(f.dev, "put a cache in front of the token store")
	if turn.FinishReason != "finished" {
		t.Fatalf("turn = %s/%s: %s", turn.Status, turn.FinishReason, turn.ErrorDetail)
	}
	id := f.onlyProposal()

	// The delete recorded what it was raised against, which is what makes a
	// later conflict detectable at all.
	raised := f.proposal(f.mgr, id)
	if len(raised.Changes) != 3 {
		t.Fatalf("got %d changes, want 3", len(raised.Changes))
	}
	if got := mapString(raised.Changes[2].Before, "relation"); got != "writes_to" {
		t.Errorf("the delete recorded before=%v; without it the premise cannot be checked",
			raised.Changes[2].Before)
	}

	f.approve(f.mgr, id, http.StatusOK)

	var revision int
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT current_revision FROM diagrams WHERE id = $1`, did).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 2 {
		t.Errorf("the diagram is at revision %d, want 2: three edits to one picture are one revision", revision)
	}

	var nodes, edges int
	if err := f.h.db.Pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM diagram_nodes WHERE diagram_id = $1 AND deleted_at IS NULL),
		       (SELECT count(*) FROM diagram_edges WHERE diagram_id = $1 AND deleted_at IS NULL)`,
		did).Scan(&nodes, &edges); err != nil {
		t.Fatal(err)
	}
	if nodes != 3 || edges != 1 {
		t.Errorf("graph = %d nodes / %d edges, want 3 / 1 — the create landed and the delete took the old arrow",
			nodes, edges)
	}
}

// TestADiagramProposalWhoseElementMovedIsSuperseded: the same staleness rule
// as the plan, over the graph.
func TestADiagramProposalWhoseElementMovedIsSuperseded(t *testing.T) {
	f := newGovFixture(t)
	did, slug := f.diagram(t)

	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "propose_diagram_change", Args: map[string]any{
			"title": "The token store is really a queue", "rationale": "it is drained, not read",
			"diagram": slug,
			"changes": []any{map[string]any{"op": "update", "target": "diagram_node",
				"after": map[string]any{"slug": "token-store", "kind": "queue"}}},
		}}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "proposed a re-kind"}}}},
	)
	if turn := f.post(f.dev, "the token store is a queue"); turn.FinishReason != "finished" {
		t.Fatalf("turn = %s/%s: %s", turn.Status, turn.FinishReason, turn.ErrorDetail)
	}
	id := f.onlyProposal()

	// A human decides otherwise first.
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+did.String()+"/revisions", map[string]any{
		"base_revision": 1, "summary": "it is a cache",
		"nodes": []map[string]any{
			{"slug": "auth-service", "kind": "service", "name": "Auth Service"},
			{"slug": "token-store", "kind": "component", "name": "Token Store"},
		},
		"edges": []map[string]any{
			{"from": "auth-service", "to": "token-store", "relation": "writes_to"},
		},
	}), http.StatusCreated, nil)

	body := f.approve(f.mgr, id, http.StatusConflict)
	msg := errMessage(body)
	for _, want := range []string{"token-store", "kind", "datastore", "component"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the conflict does not name %q:\n%s", want, msg)
		}
	}
	if got := f.proposal(f.mgr, id).Status; got != "superseded" {
		t.Errorf("status = %q, want superseded", got)
	}
}

// TestReevaluateFindingNothingIsAnAnswer: an empty correction list is a result,
// not a failure, and must not produce an empty proposal.
func TestReevaluateFindingNothingIsAnAnswer(t *testing.T) {
	f := newGovFixture(t)
	did, _ := f.diagram(t)

	f.model.script(scriptStep{Content: `{"corrections":[]}`})

	var out struct {
		Proposal *proposalView `json:"proposal"`
		Message  string        `json:"message"`
	}
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+did.String()+"/reevaluate", nil),
		http.StatusOK, &out)

	if out.Proposal != nil {
		t.Errorf("an empty answer produced a proposal: %+v", out.Proposal)
	}
	if out.Message == "" {
		t.Error("nothing was said about a run that found nothing")
	}
	if props := f.proposals(); len(props) != 0 {
		t.Errorf("an empty answer left %d proposal(s) behind", len(props))
	}
}

// lastSystemPrompt returns the system content of the most recent model call.
func (f *govFixture) lastSystemPrompt(t *testing.T) string {
	t.Helper()
	f.model.mu.Lock()
	defer f.model.mu.Unlock()
	if len(f.model.requests) == 0 {
		t.Fatal("the model was never called")
	}
	var b strings.Builder
	for _, m := range f.model.requests[len(f.model.requests)-1].messages {
		if m["role"] == "system" {
			b.WriteString(mapString(m, "content"))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func mapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// ── the ownership table stays the single source of truth ─────────────────────

// TestEverySettableFieldIsAppliable pins the applier to §4.2.
//
// The failure it exists to catch is quiet: adding a field to the ownership table
// makes it proposable immediately, and if the applier has never heard of it the
// proposal is raised, read, agreed to, and then refused — at the one moment when
// somebody has already spent their attention on it.
func TestEverySettableFieldIsAppliable(t *testing.T) {
	// What the applier in service/governance/changes.go actually reads. Kept by
	// hand *on purpose*: this is the list a person has to extend, and the
	// comparison below is what makes forgetting a test failure rather than a
	// runtime refusal.
	applied := map[pr.ChangeTarget][]string{
		pr.ChangeMilestone: {"due_date", "intent", "name", "seq", "start_date", "status"},
		pr.ChangeWorkItem: {
			"acceptance_criteria", "assignee_id", "dependencies", "due_date", "entity_refs",
			"intent", "milestone_id", "priority", "progress_pct", "start_date", "status", "title",
		},
		pr.ChangeSubTask: {"assignee_id", "seq", "status", "title"},
	}

	for target, want := range applied {
		got := governance.SettableFields(target)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("§4.2 lists %v for %s; the applier handles %v.\n"+
				"A field the table names and the applier does not is a proposal that is agreed to and then refused.",
				got, target, want)
		}
	}
}

// TestASubtaskUpdateIsRefusedAtProposeTime: §4.2 gives a SubTask no
// manager-owned field, so there is nothing on an existing one to govern — and
// saying so while the model can still act on it beats saying so after a human
// has agreed to it.
func TestASubtaskUpdateIsRefusedAtProposeTime(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry"})

	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "propose_plan_change", Args: map[string]any{
			"title": "Retitle a subtask", "rationale": "it says the wrong thing",
			"changes": []any{map[string]any{
				"op": "update", "target": "sub_task", "ref": item.Ref,
				"after": map[string]any{"title": "something else"},
			}},
		}}}},
		scriptStep{Calls: []scriptCall{{Name: "handoff_to_human", Args: map[string]any{
			"reason": "I cannot propose a subtask edit"}}}},
	)
	turn := f.post(f.dev, "retitle the subtask")
	if turn.Status != "complete" {
		t.Fatalf("turn = %s/%s: %s", turn.Status, turn.FinishReason, turn.ErrorDetail)
	}
	if props := f.proposals(); len(props) != 0 {
		t.Errorf("a subtask update became a proposal: %v", props)
	}

	if answer := f.toolAnswer(turn.ID, "propose_plan_change"); !strings.Contains(answer, "manager-owned") {
		t.Errorf("the refusal does not explain why: %s", answer)
	}
}

// toolAnswer returns what one tool told the model, whichever way it ended.
//
// Refusals split two ways on purpose: a **denial** is a policy boundary and
// escalates on repetition, while a correctable call — a field that does not
// exist, a ref that resolves to nothing — is something the model should simply
// fix. Both land in the trace; only one is a `tool_denied`.
func (f *govFixture) toolAnswer(turnID uuid.UUID, tool string) string {
	f.h.t.Helper()
	var answer string
	err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT detail->>'answer' FROM turn_events
		  WHERE turn_id = $1 AND subject = $2 AND detail ? 'answer' ORDER BY seq LIMIT 1`,
		turnID, tool).Scan(&answer)
	if err != nil {
		f.h.t.Fatalf("no answer recorded for %s on turn %s: %v", tool, turnID, err)
	}
	return answer
}

// TestAnUnknownFieldIsRefusedAtProposeTime is the same argument for a field
// that does not exist at all.
func TestAnUnknownFieldIsRefusedAtProposeTime(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry"})

	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "propose_plan_change", Args: map[string]any{
			"title": "Give it an owner", "rationale": "nobody is on it",
			"changes": []any{map[string]any{
				"op": "update", "target": "work_item", "ref": item.Ref,
				"after": map[string]any{"owner": "dev@plan.test"},
			}},
		}}}},
		scriptStep{Calls: []scriptCall{{Name: "handoff_to_human", Args: map[string]any{"reason": "no such field"}}}},
	)
	turn := f.post(f.dev, "give it an owner")
	if turn.Status != "complete" {
		t.Fatalf("turn = %s/%s", turn.Status, turn.FinishReason)
	}
	if props := f.proposals(); len(props) != 0 {
		t.Errorf("a proposal was raised with a field nothing can set: %v", props)
	}

	answer := f.toolAnswer(turn.ID, "propose_plan_change")
	for _, want := range []string{"owner", "assignee_id"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the refusal does not name %q, so the model cannot fix it: %s", want, answer)
		}
	}
}

// ── decided proposals are decided once ───────────────────────────────────────

func TestADecidedProposalIsNotReDecided(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry", "priority": "low"})
	id := f.proposePlan("Raise the priority", "it is on the critical path now",
		map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
			"after": map[string]any{"priority": "urgent"}},
	)

	f.approve(f.mgr, id, http.StatusOK)
	f.approve(f.mgr, id, http.StatusConflict)

	resp := f.mgr.do(http.MethodPost, "/api/v1/proposals/"+id.String()+"/reject",
		map[string]any{"rationale": "changed my mind after approving it"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("rejecting an approved proposal gave %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()
}

func errMessage(body map[string]any) string {
	e, ok := body["error"].(map[string]any)
	if !ok {
		raw, _ := json.Marshal(body)
		return string(raw)
	}
	msg, _ := e["message"].(string)
	return msg
}
