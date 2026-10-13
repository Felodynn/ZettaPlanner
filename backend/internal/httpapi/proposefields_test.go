package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// What a proposal is allowed to say, checked while the model can still fix it.
//
// Every case here is one that reached a reviewer in production. A proposal is
// read by a person, agreed to, and then found to be unapplicable — which spends
// their attention on nothing and sends the model back around a loop it learns
// very little from. The cost of catching these late is not a worse error
// message; it is a human deciding twice.
//
// The other half of the file is the addressing rule. Nothing an agent reads
// prints a UUID, so a field that only accepted one was a field it could not
// fill: `milestone_id` and `assignee_id` take the name, and the applier
// resolves it.

// attemptPlan runs a turn whose only act is one propose_plan_change, and
// returns what the tool said back. Used for the cases that must be refused, so
// the assertion is on the teaching text rather than on a proposal that should
// never have been raised.
func (f *govFixture) attemptPlan(changes ...map[string]any) string {
	f.h.t.Helper()
	raw := make([]any, 0, len(changes))
	for _, c := range changes {
		raw = append(raw, c)
	}
	before := len(f.proposals())
	f.model.script(
		scriptStep{Calls: []scriptCall{{Name: "propose_plan_change", Args: map[string]any{
			"title": "an attempt", "rationale": "exercising the propose-time check", "changes": raw,
		}}}},
		scriptStep{Calls: []scriptCall{{Name: "handoff_to_human", Args: map[string]any{
			"reason": "the harness refused the change"}}}},
	)
	turn := f.post(f.dev, "attempt a plan change")

	var answer string
	if err := f.h.db.Pool.QueryRow(context.Background(),
		`SELECT detail->>'answer' FROM turn_events
		  WHERE turn_id = $1 AND subject = 'propose_plan_change' AND kind IN ('tool_result', 'tool_denied')
		  ORDER BY seq LIMIT 1`, turn.ID).Scan(&answer); err != nil {
		f.h.t.Fatalf("no answer recorded for the propose call: %v", err)
	}
	if n := len(f.proposals()) - before; n != 0 {
		f.h.t.Fatalf("%d proposal(s) were raised by a call that should have been refused", n)
	}
	return answer
}

// TestAMilestoneIsNamedNotIdentified is the gap that made milestones useless.
//
// `get_plan` prints milestones by name and assembled context prints them by
// name; neither prints an id, and there is no tool that returns one. So a
// `milestone_id` that accepted only a UUID was a field the planner could not
// fill from anything it could see, and every proposal it raised left the work
// unfiled. The name is what it has, so the name is what the field takes.
func TestAMilestoneIsNamedNotIdentified(t *testing.T) {
	f := newGovFixture(t)

	id := f.proposePlan("File the retry work under v0 backend", "it is part of the substrate",
		map[string]any{"op": "create", "target": "work_item",
			"after": map[string]any{"title": "Retry the token refresh", "milestone_id": "v0 backend"}},
	)
	f.approve(f.mgr, id, http.StatusOK)

	item := f.itemByTitle("Retry the token refresh")
	if item.MilestoneID != f.msID.String() {
		t.Errorf("the item was not filed under the milestone: milestone_id = %q, want %s",
			item.MilestoneID, f.msID)
	}

	// The reviewer read a name, so a name is what the proposal still says. A
	// UUID substituted at propose time would have shown them nothing they could
	// check.
	got := f.proposal(f.mgr, id)
	if after := got.Changes[0].After["milestone_id"]; after != "v0 backend" {
		t.Errorf("the recorded change no longer says what the reviewer agreed to: %v", after)
	}
}

// TestAMilestoneAndItsWorkArriveTogether: the changes in one proposal apply in
// order inside one transaction, so a proposal may create a milestone and file
// work under it without waiting a turn for the first half to be approved.
func TestAMilestoneAndItsWorkArriveTogether(t *testing.T) {
	f := newGovFixture(t)

	id := f.proposePlan("Foundation & Infrastructure, and the work in it",
		"everything downstream depends on these",
		map[string]any{"op": "create", "target": "milestone",
			"after": map[string]any{"name": "Foundation & Infrastructure", "intent": "the substrate"}},
		map[string]any{"op": "create", "target": "work_item",
			"after": map[string]any{"title": "Repository scaffolding",
				"milestone_id": "Foundation & Infrastructure", "priority": "high"}},
		map[string]any{"op": "create", "target": "work_item",
			"after": map[string]any{"title": "CI/CD pipeline",
				"milestone_id": "foundation & infrastructure"}}, // named case-insensitively
	)
	f.approve(f.mgr, id, http.StatusOK)

	var milestones struct {
		Milestones []struct {
			ID   uuid.UUID `json:"id"`
			Name string    `json:"name"`
		} `json:"milestones"`
	}
	f.h.decode(f.mgr.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/milestones", nil),
		http.StatusOK, &milestones)
	var created uuid.UUID
	for _, m := range milestones.Milestones {
		if m.Name == "Foundation & Infrastructure" {
			created = m.ID
		}
	}
	if created == uuid.Nil {
		t.Fatal("the milestone was not created")
	}
	for _, title := range []string{"Repository scaffolding", "CI/CD pipeline"} {
		if got := f.itemByTitle(title).MilestoneID; got != created.String() {
			t.Errorf("%q was not filed under the new milestone: milestone_id = %q", title, got)
		}
	}
}

// TestAnUnknownMilestoneNamesTheOnesThatExist. The refusal has to carry the
// alternatives, because a model told only "no" guesses again.
func TestAnUnknownMilestoneNamesTheOnesThatExist(t *testing.T) {
	f := newGovFixture(t)

	answer := f.attemptPlan(map[string]any{"op": "create", "target": "work_item",
		"after": map[string]any{"title": "Repository scaffolding", "milestone_id": "Phase Zero"}})

	for _, want := range []string{"Phase Zero", "v0 backend"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the refusal does not name %q: %s", want, answer)
		}
	}
}

// TestAPriorityOutsideTheVocabularyIsRefusedAtProposeTime.
//
// `P0` is the value that actually arrived. It is a plausible priority
// everywhere except here, which is why the refusal has to print the four that
// exist and say that unset is allowed — a model that only learns "not P0"
// reaches for P1.
func TestAPriorityOutsideTheVocabularyIsRefusedAtProposeTime(t *testing.T) {
	f := newGovFixture(t)

	answer := f.attemptPlan(map[string]any{"op": "create", "target": "work_item",
		"after": map[string]any{"title": "GPU pool provisioning", "priority": "P0"}})

	for _, want := range []string{"P0", "low", "normal", "high", "urgent"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the refusal does not name %q: %s", want, answer)
		}
	}
}

// TestAMilestoneHasNoBlockedStatus: the two status vocabularies are different,
// and the change's target decides which one applies.
func TestAMilestoneHasNoBlockedStatus(t *testing.T) {
	f := newGovFixture(t)

	answer := f.attemptPlan(map[string]any{"op": "update", "target": "milestone", "ref": "v0 backend",
		"after": map[string]any{"status": "blocked"}})
	if !strings.Contains(answer, "planned") || !strings.Contains(answer, "active") {
		t.Errorf("the refusal does not teach the milestone vocabulary: %s", answer)
	}

	// The same word is fine on a work item, which is the distinction being made.
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry"})
	id := f.proposePlan("Mark it blocked", "waiting on the GPU pool",
		map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
			"after": map[string]any{"status": "blocked"}})
	f.approve(f.mgr, id, http.StatusOK)
	if got := f.getItem(f.mgr, item.ID).Status; got != "blocked" {
		t.Errorf("status = %q, want blocked", got)
	}
}

// TestAStatusCannotBeCleared. The empty string is how every other field on this
// surface says "clear it", and status has no cleared state — an item is always
// somewhere in its lifecycle. Left to the write path this failed after somebody
// had approved it, which is the whole class of bug this check exists for.
func TestAStatusCannotBeCleared(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry"})

	answer := f.attemptPlan(map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
		"after": map[string]any{"status": ""}})
	if !strings.Contains(answer, "dropped") {
		t.Errorf("the refusal does not say what to write instead: %s", answer)
	}

	// A create is different: it has a default, and saying nothing means todo.
	id := f.proposePlan("Add the observability work", "nothing is instrumented yet",
		map[string]any{"op": "create", "target": "work_item",
			"after": map[string]any{"title": "Observability stack", "status": ""}})
	f.approve(f.mgr, id, http.StatusOK)
	if got := f.itemByTitle("Observability stack").Status; got != "todo" {
		t.Errorf("status = %q, want todo", got)
	}
}

// TestEntityRefsAreAListAndTheSlugsHaveToExist.
//
// The shape is normalised and the slugs are not. A model that sent "a, b" for
// an array meant two slugs and refusing that teaches it nothing about this
// project; a slug that names no entity is a different kind of wrong, and a ref
// list that quietly dropped it is an item that silently stops appearing in that
// entity's context slice.
func TestEntityRefsAreAListAndTheSlugsHaveToExist(t *testing.T) {
	f := newGovFixture(t)
	f.buildArchitecture()

	answer := f.attemptPlan(map[string]any{"op": "create", "target": "work_item",
		"after": map[string]any{"title": "Rotate session tokens", "entity_refs": []any{"mimita-llm"}}})
	for _, want := range []string{"mimita-llm", "auth-service", "token-store"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the refusal does not name %q: %s", want, answer)
		}
	}

	id := f.proposePlan("Link the rotation work to the architecture", "it changes both",
		map[string]any{"op": "create", "target": "work_item",
			"after": map[string]any{"title": "Rotate session tokens",
				"entity_refs": "auth-service, token-store"}}, // a list written as prose
	)
	f.approve(f.mgr, id, http.StatusOK)

	got := f.getItem(f.mgr, f.itemByTitle("Rotate session tokens").ID)
	if len(got.Implements) != 2 {
		t.Fatalf("entity refs = %v, want both slugs", got.Implements)
	}
}

// TestAnAssigneeIsNamedNotIdentified, for the same reason a milestone is: no
// user id is ever printed to whoever writes the proposal.
func TestAnAssigneeIsNamedNotIdentified(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry"})

	id := f.proposePlan("Give it to Dev", "they wrote the surrounding code",
		map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
			"after": map[string]any{"assignee_id": "dev@plan.test"}})
	f.approve(f.mgr, id, http.StatusOK)

	if got := f.getItem(f.mgr, item.ID).AssigneeID; got != f.dev.userID.String() {
		t.Errorf("assignee_id = %q, want %s", got, f.dev.userID)
	}

	answer := f.attemptPlan(map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
		"after": map[string]any{"assignee_id": "Somebody Else"}})
	if !strings.Contains(answer, "Somebody Else") || !strings.Contains(answer, "dev@plan.test") {
		t.Errorf("the refusal does not name who is actually on the project: %s", answer)
	}
}

// TestADependencyKindThatDoesNotExistIsRefusedRatherThanDropped.
//
// The applier reads `blocks` and `relates`. A proposal saying `blocked_by`
// would once have applied cleanly, reported success and wired nothing — the
// failure a reviewer has no way to see, because the diff they approved still
// says it.
func TestADependencyKindThatDoesNotExistIsRefusedRatherThanDropped(t *testing.T) {
	f := newGovFixture(t)
	one := f.createItem(f.mgr, map[string]any{"title": "Session expiry"})
	two := f.createItem(f.mgr, map[string]any{"title": "Token rotation"})

	answer := f.attemptPlan(map[string]any{"op": "update", "target": "work_item", "ref": one.Ref,
		"after": map[string]any{"dependencies": map[string]any{"blocked_by": []any{two.Ref}}}})
	if !strings.Contains(answer, "blocked_by") {
		t.Errorf("the refusal does not name the key that was wrong: %s", answer)
	}
	if !strings.Contains(answer, "blocks") || !strings.Contains(answer, "relates") {
		t.Errorf("the refusal does not teach the two kinds there are: %s", answer)
	}
	// And it says which way round to write it, because that is the actual fix.
	if !strings.Contains(answer, two.Ref) {
		t.Errorf("the refusal does not say to propose it from the other item: %s", answer)
	}
}

// TestAFieldThatIsNotAFieldNamesTheOnesThatAre. The oldest of these checks, kept
// because it is the one that proves the list is read from the ownership table
// rather than written out beside it.
func TestAFieldThatIsNotAFieldNamesTheOnesThatAre(t *testing.T) {
	f := newGovFixture(t)

	answer := f.attemptPlan(map[string]any{"op": "create", "target": "work_item",
		"after": map[string]any{"title": "Observability stack", "owner": "platform team"}})
	if !strings.Contains(answer, "owner") {
		t.Errorf("the refusal does not name the field: %s", answer)
	}
	for _, want := range []string{"acceptance_criteria", "entity_refs", "milestone_id"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the refusal does not list %q as settable: %s", want, answer)
		}
	}
}

// TestACreateWithNothingInItIsRefused, and so is an update that changes
// nothing: it would apply cleanly and report success having done nothing, which
// is the one outcome worse than a refusal.
func TestACreateWithNothingInItIsRefused(t *testing.T) {
	f := newGovFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Session expiry"})

	if answer := f.attemptPlan(map[string]any{"op": "create", "target": "work_item",
		"after": map[string]any{"intent": "something"}}); !strings.Contains(answer, "title") {
		t.Errorf("a titleless work item was not refused for its title: %s", answer)
	}
	if answer := f.attemptPlan(map[string]any{"op": "update", "target": "work_item", "ref": item.Ref,
		"after": map[string]any{}}); !strings.Contains(answer, "without saying what to change") {
		t.Errorf("an empty update was not refused: %s", answer)
	}
}

// ── fixture helpers ──────────────────────────────────────────────────────────

// itemByTitle finds a work item the plan now contains. Creates through a
// proposal have no ref until they land, so the title is the only handle a test
// has on one.
func (f *govFixture) itemByTitle(title string) itemView {
	f.h.t.Helper()
	var out struct {
		Items []itemView `json:"work_items"`
	}
	f.h.decode(f.mgr.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/work-items", nil),
		http.StatusOK, &out)
	for _, it := range out.Items {
		if it.Title == title {
			return it
		}
	}
	f.h.t.Fatalf("no work item titled %q", title)
	return itemView{}
}

// buildArchitecture puts two real entities in the project, through the Phase 2
// path rather than by insert, so the slugs a proposal names are rows.
func (f *govFixture) buildArchitecture() {
	f.h.t.Helper()
	var diagram struct {
		ID uuid.UUID `json:"id"`
	}
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/diagrams",
		map[string]any{"name": "Auth Context", "level": "context"}), http.StatusCreated, &diagram)
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+diagram.ID.String()+"/lease", nil),
		http.StatusOK, nil)
	f.h.decode(f.mgr.do(http.MethodPost, "/api/v1/diagrams/"+diagram.ID.String()+"/revisions", map[string]any{
		"base_revision": 0,
		"summary":       "initial",
		"nodes": []map[string]any{
			{"slug": "auth-service", "kind": "service", "name": "Auth Service"},
			{"slug": "token-store", "kind": "datastore", "name": "Token Store"},
		},
		"edges": []map[string]any{},
	}), http.StatusCreated, nil)
}
