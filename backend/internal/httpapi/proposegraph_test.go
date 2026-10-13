package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The same argument as proposefields_test.go, applied to the graph.
//
// A plan change is a set of fields on a row. A graph change is a statement
// about a *shape*: an arrow needs both its ends to be boxes, a box inside a
// boundary needs the boundary to exist and to be the sort of thing that can
// hold one. Those are answerable at propose time against the picture the tool
// has already read — and left to apply time they fail after a reviewer has
// agreed to a diagram that cannot be drawn.

// attemptDiagram runs a turn whose only act is one propose_diagram_change that
// is expected to be refused, and returns what the tool said back.
func (f *govFixture) attemptDiagram(slug string, changes ...map[string]any) string {
	f.h.t.Helper()
	raw := make([]any, 0, len(changes))
	for _, c := range changes {
		raw = append(raw, c)
	}
	before := len(f.proposals())
	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "propose_diagram_change", Args: map[string]any{
			"title": "an attempt", "rationale": "exercising the propose-time check",
			"diagram": slug, "changes": raw,
		}}}},
		scriptStep{Calls: []scriptCall{{Name: "handoff_to_human", Args: map[string]any{
			"reason": "the harness refused the change"}}}},
	)
	turn := f.post(f.dev, "attempt a diagram change")

	var answer string
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT detail->>'answer' FROM turn_events
		  WHERE turn_id = $1 AND subject = 'propose_diagram_change' AND kind IN ('tool_result', 'tool_denied')
		  ORDER BY seq LIMIT 1`, turn.ID).Scan(&answer); err != nil {
		f.h.t.Fatalf("no answer recorded for the propose call: %v", err)
	}
	if n := len(f.proposals()) - before; n != 0 {
		f.h.t.Fatalf("%d proposal(s) were raised by a call that should have been refused", n)
	}
	return answer
}

// TestAMissingDiagramIsNotAnInvitationToCreateOne.
//
// The refusal used to end "or propose creating this one with target `diagram`",
// which is impossible twice over: the tool refuses before it reads `changes` at
// all, and the applier rejects any op but `update` on the picture itself. An
// instruction the harness will refuse on the next call is worse than none — the
// model follows it, is refused again, and has nowhere left to go.
func TestAMissingDiagramIsNotAnInvitationToCreateOne(t *testing.T) {
	f := newGovFixture(t)
	_, slug := f.diagram(t)

	answer := f.attemptDiagram("deployment-view",
		map[string]any{"op": "create", "target": "diagram_node",
			"after": map[string]any{"slug": "gpu-pool", "kind": "service", "name": "GPU Pool"}})

	if strings.Contains(answer, "propose creating this one") {
		t.Errorf("the refusal still tells the model to do something that cannot be done: %s", answer)
	}
	if !strings.Contains(answer, slug) {
		t.Errorf("the refusal does not name the diagrams that do exist: %s", answer)
	}
	if !strings.Contains(answer, "canvas") {
		t.Errorf("the refusal does not say where a diagram actually comes from: %s", answer)
	}

	// And the op is refused explicitly when the diagram does exist, rather than
	// being accepted here and rejected after somebody approves it.
	answer = f.attemptDiagram(slug,
		map[string]any{"op": "create", "target": "diagram", "after": map[string]any{"name": "Deployment"}})
	if !strings.Contains(answer, "update") {
		t.Errorf("the refusal does not name the one op that works on a diagram: %s", answer)
	}
}

// TestAFieldTheGraphDoesNotHaveIsRefusedRatherThanDropped.
//
// `graphEdit` reads five keys off a node and ignores the rest, so a proposal
// carrying `technology` applied cleanly, reported success and dropped it — and
// the proposal on screen still says it, so there is nothing for the reviewer to
// notice. The plan side has had this check since D55; the graph side did not.
func TestAFieldTheGraphDoesNotHaveIsRefusedRatherThanDropped(t *testing.T) {
	f := newGovFixture(t)
	_, slug := f.diagram(t)

	answer := f.attemptDiagram(slug,
		map[string]any{"op": "create", "target": "diagram_node", "after": map[string]any{
			"slug": "session-cache", "kind": "datastore", "name": "Session Cache",
			"technology": "Redis 7", "owner": "platform"}})

	for _, want := range []string{"technology", "owner", "summary"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the refusal does not mention %q: %s", want, answer)
		}
	}
}

// TestANodeCannotBeCreatedWithoutAKind. Every node is a placement of a catalog
// entity and every entity has a kind, so a create without one fails graph
// validation — after approval, where it costs a decision.
func TestANodeCannotBeCreatedWithoutAKind(t *testing.T) {
	f := newGovFixture(t)
	_, slug := f.diagram(t)

	answer := f.attemptDiagram(slug,
		map[string]any{"op": "create", "target": "diagram_node",
			"after": map[string]any{"slug": "session-cache", "name": "Session Cache"}})
	if !strings.Contains(answer, "kind") || !strings.Contains(answer, "datastore") {
		t.Errorf("the refusal does not say what is missing or what the kinds are: %s", answer)
	}

	// An update is different: it changes only what it names, so a node that
	// already has a kind keeps it.
	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "propose_diagram_change", Args: map[string]any{
			"title": "Rename the store", "rationale": "it holds sessions, not tokens", "diagram": slug,
			"changes": []any{map[string]any{"op": "update", "target": "diagram_node",
				"after": map[string]any{"slug": "token-store", "name": "Session Store"}}},
		}}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "proposed a rename"}}}},
	)
	if turn := f.post(f.dev, "rename the store"); turn.FinishReason != "finished" {
		t.Fatalf("a kindless update was refused: %s", turn.ErrorDetail)
	}
}

// TestAnArrowNeedsBothItsEndsToBeBoxes, and one of them may be a box this same
// proposal draws — the edits compose in order onto the graph.
func TestAnArrowNeedsBothItsEndsToBeBoxes(t *testing.T) {
	f := newGovFixture(t)
	_, slug := f.diagram(t)

	answer := f.attemptDiagram(slug,
		map[string]any{"op": "create", "target": "diagram_edge", "after": map[string]any{
			"from": "auth-service", "to": "session-cache", "relation": "reads_from"}})
	for _, want := range []string{"session-cache", "auth-service", "token-store"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the refusal does not name %q: %s", want, answer)
		}
	}

	// Drawing the box first, in the same proposal, is the fix — and it works.
	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "propose_diagram_change", Args: map[string]any{
			"title": "Cache the read path", "rationale": "the store is the bottleneck", "diagram": slug,
			"changes": []any{
				map[string]any{"op": "create", "target": "diagram_node", "after": map[string]any{
					"slug": "session-cache", "kind": "datastore", "name": "Session Cache"}},
				map[string]any{"op": "create", "target": "diagram_edge", "after": map[string]any{
					"from": "auth-service", "to": "session-cache", "relation": "reads_from"}},
			},
		}}}},
		scriptStep{Calls: []scriptCall{{Name: "finish", Args: map[string]any{"summary": "proposed a cache"}}}},
	)
	turn := f.post(f.dev, "cache the read path")
	if turn.FinishReason != "finished" {
		t.Fatalf("an arrow onto a node the same proposal creates was refused: %s", turn.ErrorDetail)
	}
	f.approve(f.mgr, f.onlyProposal(), http.StatusOK)
}

// TestOnlyABoundaryOrASystemContainsAnything. Containment is the one graph rule
// a model reliably gets wrong, and the failure is a validation error thrown
// after the whole revision has been composed.
func TestOnlyABoundaryOrASystemContainsAnything(t *testing.T) {
	f := newGovFixture(t)
	_, slug := f.diagram(t)

	answer := f.attemptDiagram(slug,
		map[string]any{"op": "create", "target": "diagram_node", "after": map[string]any{
			"slug": "session-cache", "kind": "datastore", "name": "Session Cache",
			"parent": "token-store"}})
	if !strings.Contains(answer, "datastore") || !strings.Contains(answer, "boundary") {
		t.Errorf("the refusal does not say why the parent cannot contain it: %s", answer)
	}

	answer = f.attemptDiagram(slug,
		map[string]any{"op": "create", "target": "diagram_node", "after": map[string]any{
			"slug": "session-cache", "kind": "datastore", "name": "Session Cache",
			"parent": "the-platform"}})
	if !strings.Contains(answer, "the-platform") {
		t.Errorf("the refusal does not name the parent that is missing: %s", answer)
	}
}

// TestACreateThatNamesSomethingIsRefused.
//
// The applier reads `create` and ignores the ref, so this made a second work
// item — while the recorded `before` made the proposal render as an edit to the
// one named. A reviewer approves what reads as a title change and gets a
// duplicate. Both readings are plausible, so neither is guessed.
func TestACreateThatNamesSomethingIsRefused(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry"})

	answer := f.attemptPlan(map[string]any{"op": "create", "target": "work_item", "ref": item.Ref,
		"after": map[string]any{"title": "Session expiry and renewal"}})

	if !strings.Contains(answer, item.Ref) {
		t.Errorf("the refusal does not name the ref that made it ambiguous: %s", answer)
	}
	if !strings.Contains(answer, "update") {
		t.Errorf("the refusal does not name the op that was probably meant: %s", answer)
	}

	var n int
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM work_items WHERE project_id = $1 AND deleted_at IS NULL`, f.pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d work items exist, want 1: the create was not supposed to happen at all", n)
	}
}

// TestAStatusFilterOutsideTheVocabularyIsRefused.
//
// It matched no rows and the tool answered "(none matching)", which reads as a
// fact about the project rather than about the filter — so the model concludes
// there is no work in progress and acts on it. A wrong answer is worse than an
// error, and this one was indistinguishable from a true one.
func TestAStatusFilterOutsideTheVocabularyIsRefused(t *testing.T) {
	f := newGovFixture(t)
	f.createItem(f.mgr, map[string]any{"title": "Session expiry", "status": "in_progress"})

	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "get_plan", Args: map[string]any{"status": "in progress"}}}},
		scriptStep{Calls: []scriptCall{{Name: "handoff_to_human", Args: map[string]any{
			"reason": "the filter was refused"}}}},
	)
	turn := f.post(f.dev, "what is in progress")

	var answer string
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT detail->>'answer' FROM turn_events
		  WHERE turn_id = $1 AND subject = 'get_plan' AND kind IN ('tool_result', 'tool_denied')
		  ORDER BY seq LIMIT 1`, turn.ID).Scan(&answer); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(answer, "none matching") {
		t.Errorf("a bad filter still answers as though the project were empty: %s", answer)
	}
	if !strings.Contains(answer, "in_progress") {
		t.Errorf("the refusal does not name the spelling that works: %s", answer)
	}
}
