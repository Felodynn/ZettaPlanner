package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
)

// Phase 4's exit criteria, over the real HTTP surface and a real Postgres.

// planFixture is a project with an admin, a manager and two developers, plus a
// milestone. Two developers rather than one: half the ownership rules are about
// the difference between your own item and somebody else's, and a fixture with
// one developer cannot express it.
type planFixture struct {
	h     *harness
	admin *client
	mgr   *client
	dev   *client
	other *client
	pid   uuid.UUID
	msID  uuid.UUID
}

func newPlanFixture(t *testing.T) *planFixture {
	t.Helper()
	return buildPlanFixture(t, newHarness(t))
}

// buildPlanFixture populates an existing harness, so the Phase 6 tests can
// stand the same project up behind a server that also has an agent wired in.
func buildPlanFixture(t *testing.T, h *harness) *planFixture {
	t.Helper()
	admin := h.bootstrapAdmin()
	mgr := admin.createUser("mgr@plan.test", "Manager", policy.RoleManager)
	dev := admin.createUser("dev@plan.test", "Dev", policy.RoleDeveloper)
	other := admin.createUser("other@plan.test", "Other Dev", policy.RoleDeveloper)

	pid := h.createProject(admin, "ZP", "ZettaPlanner")
	for _, m := range []struct {
		c    *client
		role string
	}{{mgr, "manager"}, {dev, "developer"}, {other, "developer"}} {
		h.decode(admin.do(http.MethodPut,
			"/api/v1/projects/"+pid.String()+"/members/"+m.c.userID.String(),
			map[string]any{"role": m.role}), http.StatusNoContent, nil)
	}

	f := &planFixture{h: h, admin: admin, mgr: mgr, dev: dev, other: other, pid: pid}
	f.msID = f.createMilestone(mgr, map[string]any{
		"name": "v0 backend", "intent": "the substrate works end to end", "due_date": "2026-09-30",
	})
	return f
}

type itemView struct {
	ID          uuid.UUID `json:"id"`
	Ref         string    `json:"ref"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	ProgressPct int       `json:"progress_pct"`
	Priority    string    `json:"priority"`
	AssigneeID  string    `json:"assignee_id"`
	MilestoneID string    `json:"milestone_id"`
	StartDate   string    `json:"start_date"`
	DueDate     string    `json:"due_date"`
	Provenance  string    `json:"provenance"`
	DeletedAt   string    `json:"deleted_at"`

	SubTasks []struct {
		ID     uuid.UUID `json:"id"`
		Title  string    `json:"title"`
		Status string    `json:"status"`
	} `json:"sub_tasks"`
	Checklist []struct {
		ID      uuid.UUID `json:"id"`
		Text    string    `json:"text"`
		Checked bool      `json:"checked"`
	} `json:"checklist"`
	Blocks []struct {
		Ref string `json:"ref"`
	} `json:"blocks"`
	BlockedBy []struct {
		Ref string `json:"ref"`
	} `json:"blocked_by"`
	Implements []struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"implements"`
}

func (f *planFixture) createMilestone(c *client, body map[string]any) uuid.UUID {
	f.h.t.Helper()
	var out struct {
		ID uuid.UUID `json:"id"`
	}
	f.h.decode(c.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/milestones", body),
		http.StatusCreated, &out)
	return out.ID
}

func (f *planFixture) createItem(c *client, body map[string]any) itemView {
	f.h.t.Helper()
	var out itemView
	f.h.decode(c.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/work-items", body),
		http.StatusCreated, &out)
	return out
}

func (f *planFixture) getItem(c *client, id uuid.UUID) itemView {
	f.h.t.Helper()
	var out itemView
	f.h.decode(c.do(http.MethodGet, "/api/v1/work-items/"+id.String(), nil), http.StatusOK, &out)
	return out
}

func (f *planFixture) patchItem(c *client, id uuid.UUID, body map[string]any) *http.Response {
	return c.do(http.MethodPatch, "/api/v1/work-items/"+id.String(), body)
}

// auditActions returns every action recorded against a subject, oldest first.
func (f *planFixture) auditActions(subject uuid.UUID) []string {
	f.h.t.Helper()
	rows, err := f.h.db.Pool.Query(context.Background(),
		`SELECT action FROM audit_log WHERE subject_id = $1 ORDER BY at, id`, subject)
	if err != nil {
		f.h.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			f.h.t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// ── the exit criteria ────────────────────────────────────────────────────────

// TestFieldOwnershipIsEnforcedPerField is the first exit criterion and the
// fourth at once: full CRUD with ownership checked field by field, and a
// developer able to write dev-owned fields on assigned items only.
func TestFieldOwnershipIsEnforcedPerField(t *testing.T) {
	f := newPlanFixture(t)

	// Creating a work item is manager-owned.
	resp := f.dev.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/work-items",
		map[string]any{"title": "a developer should not be able to make this"})
	body := f.h.decodeErr(resp, http.StatusForbidden)
	if !strings.Contains(body.Error.Message, "raise a proposal") {
		t.Errorf("the refusal should name the route forward: %q", body.Error.Message)
	}

	mine := f.createItem(f.mgr, map[string]any{
		"title": "Wire the plan model", "assignee_id": f.dev.userID.String(),
		"milestone_id": f.msID.String(), "priority": "high",
	})
	theirs := f.createItem(f.mgr, map[string]any{
		"title": "Something else", "assignee_id": f.other.userID.String(),
	})

	// Dev-owned, on an item assigned to the caller.
	f.h.decode(f.patchItem(f.dev, mine.ID, map[string]any{"status": "in_progress", "progress_pct": 40}),
		http.StatusOK, nil)

	// The same field on somebody else's item.
	body = f.h.decodeErr(f.patchItem(f.dev, theirs.ID, map[string]any{"status": "done"}), http.StatusForbidden)
	if body.Error.Code != "field_not_owned" || body.Error.Details["field"] != "status" {
		t.Errorf("expected field_not_owned on status, got %s / %v", body.Error.Code, body.Error.Details)
	}

	// Manager-owned, attempted by the assignee.
	body = f.h.decodeErr(f.patchItem(f.dev, mine.ID, map[string]any{"due_date": "2026-10-01"}), http.StatusForbidden)
	if body.Error.Details["owning_role"] != "manager" {
		t.Errorf("the denial should name the owning role: %v", body.Error.Details)
	}

	// Not a ladder. A manager does not acquire status by outranking a developer.
	body = f.h.decodeErr(f.patchItem(f.mgr, mine.ID, map[string]any{"status": "done"}), http.StatusForbidden)
	if body.Error.Details["owning_role"] != "developer" {
		t.Errorf("a manager writing status should be told it is developer-owned: %v", body.Error.Details)
	}

	// A patch mixing an allowed and a disallowed field is refused whole, and
	// nothing from the allowed half lands.
	f.h.decodeErr(f.patchItem(f.dev, mine.ID, map[string]any{"status": "review", "title": "renamed"}),
		http.StatusForbidden)
	after := f.getItem(f.dev, mine.ID)
	if after.Status != "in_progress" || after.Title != "Wire the plan model" {
		t.Errorf("a refused patch left something behind: status=%q title=%q", after.Status, after.Title)
	}
}

// TestDependencyCyclesAreRejectedWithThePathNamed is the second exit criterion.
func TestDependencyCyclesAreRejectedWithThePathNamed(t *testing.T) {
	f := newPlanFixture(t)
	a := f.createItem(f.mgr, map[string]any{"title": "A"})
	b := f.createItem(f.mgr, map[string]any{"title": "B"})
	c := f.createItem(f.mgr, map[string]any{"title": "C"})

	setBlocks := func(from itemView, to ...uuid.UUID) *http.Response {
		ids := []string{}
		for _, id := range to {
			ids = append(ids, id.String())
		}
		return f.mgr.do(http.MethodPut, "/api/v1/work-items/"+from.ID.String()+"/dependencies",
			map[string]any{"blocks": ids})
	}

	f.h.decode(setBlocks(a, b.ID), http.StatusOK, nil)
	f.h.decode(setBlocks(b, c.ID), http.StatusOK, nil)

	// C blocks A closes the ring.
	body := f.h.decodeErr(setBlocks(c, a.ID), http.StatusBadRequest)
	if body.Error.Code != "validation_failed" {
		t.Errorf("code = %q, want validation_failed", body.Error.Code)
	}
	path, ok := body.Error.Details["cycle"].([]any)
	if !ok || len(path) < 3 {
		t.Fatalf("the error should carry the offending path: %v", body.Error.Details)
	}
	if path[0] != path[len(path)-1] {
		t.Errorf("the path should close on itself, got %v", path)
	}
	for _, ref := range []string{a.Ref, b.Ref, c.Ref} {
		if !strings.Contains(body.Error.Message, ref) {
			t.Errorf("the message does not name %s: %s", ref, body.Error.Message)
		}
	}

	// The rejected write left nothing behind: C still blocks nothing.
	if got := f.getItem(f.mgr, c.ID); len(got.Blocks) != 0 {
		t.Errorf("the rejected dependency was still written: %v", got.Blocks)
	}

	// An item cannot block itself, and `relates` may loop freely — it carries
	// no ordering, so a ring in it contradicts nothing.
	f.h.decodeErr(setBlocks(a, a.ID), http.StatusBadRequest)
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+a.ID.String()+"/dependencies",
		map[string]any{"relates": []string{b.ID.String()}}), http.StatusOK, nil)
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+b.ID.String()+"/dependencies",
		map[string]any{"relates": []string{a.ID.String()}}), http.StatusOK, nil)
}

// TestWarningsAreComputedAndNothingCascades is the third exit criterion, plus
// the rule that no date is ever adjusted automatically.
func TestWarningsAreComputedAndNothingCascades(t *testing.T) {
	f := newPlanFixture(t)

	blocker := f.createItem(f.mgr, map[string]any{
		"title": "Schema", "start_date": "2026-08-01", "due_date": "2026-08-20",
	})
	blocked := f.createItem(f.mgr, map[string]any{
		"title": "Handlers", "start_date": "2026-08-10", "due_date": "2026-08-25",
	})
	late := f.createItem(f.mgr, map[string]any{
		"title": "Docs", "milestone_id": f.msID.String(), "due_date": "2026-10-15",
	})

	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+blocker.ID.String()+"/dependencies",
		map[string]any{"blocks": []string{blocked.ID.String()}}), http.StatusOK, nil)

	type warning struct {
		Kind        string `json:"kind"`
		ItemRef     string `json:"item_ref"`
		RelatedKind string `json:"related_kind"`
		RelatedRef  string `json:"related_ref"`
		Days        int    `json:"days"`
		Message     string `json:"message"`
	}
	warnings := func() []warning {
		t.Helper()
		var out struct {
			Warnings []warning `json:"warnings"`
		}
		f.h.decode(f.dev.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/warnings", nil),
			http.StatusOK, &out)
		return out.Warnings
	}

	got := warnings()
	if len(got) != 2 {
		t.Fatalf("expected a blocker-date and a milestone-date warning, got %+v", got)
	}
	var sawBlocker, sawMilestone bool
	for _, w := range got {
		switch w.Kind {
		case "blocker_date":
			sawBlocker = true
			if w.ItemRef != blocked.Ref || w.RelatedRef != blocker.Ref {
				t.Errorf("the blocker warning names the wrong pair: %+v", w)
			}
			if w.Days != 10 {
				t.Errorf("days = %d, want 10 (2026-08-10 to 2026-08-20)", w.Days)
			}
		case "milestone_date":
			sawMilestone = true
			if w.ItemRef != late.Ref || w.RelatedKind != "milestone" {
				t.Errorf("the milestone warning names the wrong pair: %+v", w)
			}
		}
		if !strings.Contains(w.Message, w.ItemRef) {
			t.Errorf("a warning message does not name its item: %q", w.Message)
		}
	}
	if !sawBlocker || !sawMilestone {
		t.Errorf("missing a warning kind: blocker=%v milestone=%v", sawBlocker, sawMilestone)
	}

	// Nothing cascaded. Moving the blocker's due date must not have moved the
	// blocked item, and the warning must disappear purely because it is
	// recomputed — no reconciliation ran.
	f.h.decode(f.patchItem(f.mgr, blocker.ID, map[string]any{"due_date": "2026-08-05"}), http.StatusOK, nil)
	if got := f.getItem(f.mgr, blocked.ID); got.StartDate != "2026-08-10" {
		t.Errorf("the blocked item's start date moved on its own: %q", got.StartDate)
	}
	for _, w := range warnings() {
		if w.Kind == "blocker_date" {
			t.Errorf("the blocker warning survived the date it was about: %+v", w)
		}
	}

	// A finished blocker is not a violation whatever its dates say.
	f.h.decode(f.patchItem(f.mgr, blocker.ID, map[string]any{"due_date": "2026-08-20"}), http.StatusOK, nil)
	if len(warnings()) != 2 {
		t.Fatal("expected the blocker warning back after restoring the date")
	}
	f.h.decode(f.patchItem(f.mgr, blocker.ID, map[string]any{"assignee_id": f.dev.userID.String()}), http.StatusOK, nil)
	f.h.decode(f.patchItem(f.dev, blocker.ID, map[string]any{"status": "done"}), http.StatusOK, nil)
	for _, w := range warnings() {
		if w.Kind == "blocker_date" {
			t.Errorf("a done blocker still produced a warning: %+v", w)
		}
	}
}

// TestEntityRefsAreExplicitAndNeverDerived is the fifth exit criterion.
func TestEntityRefsAreExplicitAndNeverDerived(t *testing.T) {
	f := newPlanFixture(t)

	// Build a small architecture through the Phase 2 path, so the entities are
	// real rows rather than fixtures.
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

	var entities struct {
		Entities []struct {
			ID   uuid.UUID `json:"id"`
			Slug string    `json:"slug"`
		} `json:"entities"`
	}
	f.h.decode(f.mgr.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/entities", nil),
		http.StatusOK, &entities)
	bySlug := map[string]uuid.UUID{}
	for _, e := range entities.Entities {
		bySlug[e.Slug] = e.ID
	}

	// An item whose title is exactly an entity's name. Nothing links them: the
	// reference is a claim somebody makes, not something the system infers, and
	// a guessed one pulls the wrong architecture into the next conversation
	// while looking exactly like one that was meant.
	implicit := f.createItem(f.mgr, map[string]any{"title": "Auth Service"})
	if got := f.getItem(f.mgr, implicit.ID); len(got.Implements) != 0 {
		t.Errorf("a reference was derived from the title: %v", got.Implements)
	}

	explicit := f.createItem(f.mgr, map[string]any{"title": "Rotate session tokens"})
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+explicit.ID.String()+"/entity-refs",
		map[string]any{"implements": []string{bySlug["auth-service"].String(), bySlug["token-store"].String()}}),
		http.StatusOK, nil)

	got := f.getItem(f.mgr, explicit.ID)
	if len(got.Implements) != 2 {
		t.Fatalf("expected two references, got %v", got.Implements)
	}

	// Replace, not merge: a PUT is a complete statement.
	f.h.decode(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+explicit.ID.String()+"/entity-refs",
		map[string]any{"implements": []string{bySlug["auth-service"].String()}}), http.StatusOK, nil)
	if got := f.getItem(f.mgr, explicit.ID); len(got.Implements) != 1 || got.Implements[0].Slug != "auth-service" {
		t.Errorf("the PUT did not replace the set: %v", got.Implements)
	}

	// A reference to something that is not in this project names it rather
	// than surfacing a constraint violation.
	f.h.decodeErr(f.mgr.do(http.MethodPut, "/api/v1/work-items/"+explicit.ID.String()+"/entity-refs",
		map[string]any{"implements": []string{uuid.Must(uuid.NewV7()).String()}}), http.StatusNotFound)

	// Entity refs are manager-owned.
	f.h.decodeErr(f.dev.do(http.MethodPut, "/api/v1/work-items/"+explicit.ID.String()+"/entity-refs",
		map[string]any{"implements": []string{}}), http.StatusForbidden)
}

// TestEveryStatusChangeAndTickIsATransition is the sixth exit criterion.
//
// This is the phase where the retrospective's data is actually generated. A
// transition recorded as a generic update is a transition the v0.1 export
// cannot see, and it cannot be backfilled.
func TestEveryStatusChangeAndTickIsATransition(t *testing.T) {
	f := newPlanFixture(t)
	item := f.createItem(f.mgr, map[string]any{
		"title": "Ship the plan model", "assignee_id": f.dev.userID.String(),
	})

	f.h.decode(f.patchItem(f.dev, item.ID, map[string]any{"status": "in_progress"}), http.StatusOK, nil)
	f.h.decode(f.patchItem(f.dev, item.ID, map[string]any{"status": "review"}), http.StatusOK, nil)
	f.h.decode(f.patchItem(f.mgr, item.ID, map[string]any{"assignee_id": f.other.userID.String()}), http.StatusOK, nil)
	f.h.decode(f.patchItem(f.mgr, item.ID, map[string]any{"milestone_id": f.msID.String()}), http.StatusOK, nil)
	f.h.decode(f.patchItem(f.mgr, item.ID, map[string]any{"title": "Ship it"}), http.StatusOK, nil)

	actions := f.auditActions(item.ID)
	for _, want := range []string{
		"work_item.created", "work_item.status_changed", "work_item.assigned",
		"work_item.milestone_changed", "work_item.updated",
	} {
		if !contains(actions, want) {
			t.Errorf("missing %s; recorded: %v", want, actions)
		}
	}
	// A status-only patch must not also claim a generic edit happened.
	var statusChanges, updates int
	for _, a := range actions {
		switch a {
		case "work_item.status_changed":
			statusChanges++
		case "work_item.updated":
			updates++
		}
	}
	if statusChanges != 2 {
		t.Errorf("status_changed recorded %d times, want 2", statusChanges)
	}
	if updates != 1 {
		t.Errorf("work_item.updated recorded %d times, want 1 — a status-only patch must not write one", updates)
	}

	// Both directions of a checklist tick. A box ticked and then cleared is a
	// signal; recording only the tick hides it.
	var box struct {
		ID uuid.UUID `json:"id"`
	}
	f.h.decode(f.other.do(http.MethodPost, "/api/v1/work-items/"+item.ID.String()+"/checklist",
		map[string]any{"text": "migration reviewed"}), http.StatusCreated, &box)
	f.h.decode(f.other.do(http.MethodPatch, "/api/v1/checklist/"+box.ID.String(),
		map[string]any{"checked": true}), http.StatusOK, nil)
	f.h.decode(f.other.do(http.MethodPatch, "/api/v1/checklist/"+box.ID.String(),
		map[string]any{"checked": false}), http.StatusOK, nil)

	ticks := 0
	for _, a := range f.auditActions(box.ID) {
		if a == "checklist.checked" {
			ticks++
		}
	}
	if ticks != 2 {
		t.Errorf("checklist.checked recorded %d times; the un-tick is as much a signal as the tick", ticks)
	}
	var directions []bool
	rows, err := f.h.db.Pool.Query(context.Background(),
		`SELECT (after->>'checked')::bool FROM audit_log
		  WHERE subject_id = $1 AND action = 'checklist.checked' ORDER BY at, id`, box.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v bool
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		directions = append(directions, v)
	}
	if len(directions) != 2 || !directions[0] || directions[1] {
		t.Errorf("before/after does not carry which way the box went: %v", directions)
	}

	// Subtasks and milestones move through their own transitions too.
	var sub struct {
		ID uuid.UUID `json:"id"`
	}
	f.h.decode(f.other.do(http.MethodPost, "/api/v1/work-items/"+item.ID.String()+"/sub-tasks",
		map[string]any{"title": "write the migration"}), http.StatusCreated, &sub)
	f.h.decode(f.other.do(http.MethodPatch, "/api/v1/sub-tasks/"+sub.ID.String(),
		map[string]any{"status": "done"}), http.StatusOK, nil)
	if !contains(f.auditActions(sub.ID), "sub_task.status_changed") {
		t.Errorf("a subtask status move was not recorded as a transition: %v", f.auditActions(sub.ID))
	}

	f.h.decode(f.mgr.do(http.MethodPatch, "/api/v1/milestones/"+f.msID.String(),
		map[string]any{"status": "active"}), http.StatusOK, nil)
	if !contains(f.auditActions(f.msID), "milestone.status_changed") {
		t.Errorf("a milestone status move was not recorded as a transition: %v", f.auditActions(f.msID))
	}
}

// TestSoftDeleteKeepsHistoryQueryable is the seventh exit criterion.
func TestSoftDeleteKeepsHistoryQueryable(t *testing.T) {
	f := newPlanFixture(t)
	item := f.createItem(f.mgr, map[string]any{
		"title": "Doomed", "assignee_id": f.dev.userID.String(),
	})
	f.h.decode(f.patchItem(f.dev, item.ID, map[string]any{"status": "in_progress"}), http.StatusOK, nil)
	f.h.decode(f.dev.do(http.MethodPost, "/api/v1/work-items/"+item.ID.String()+"/worklogs",
		map[string]any{"kind": "progress", "body": "started, then we changed our minds"}), http.StatusCreated, nil)

	f.h.decode(f.mgr.do(http.MethodDelete, "/api/v1/work-items/"+item.ID.String(), nil),
		http.StatusNoContent, nil)

	// The row is still there, and says so.
	after := f.getItem(f.mgr, item.ID)
	if after.DeletedAt == "" {
		t.Error("a deleted item should carry deleted_at rather than vanishing")
	}
	if after.Ref != item.Ref {
		t.Errorf("the ref changed on delete: %q then %q", item.Ref, after.Ref)
	}

	// It is out of the list.
	var list struct {
		WorkItems []itemView `json:"work_items"`
	}
	f.h.decode(f.mgr.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/work-items", nil),
		http.StatusOK, &list)
	for _, w := range list.WorkItems {
		if w.ID == item.ID {
			t.Error("a deleted item is still in the list")
		}
	}

	// Its history is intact, including the worklog, which is never deleted.
	actions := f.auditActions(item.ID)
	for _, want := range []string{"work_item.created", "work_item.status_changed", "work_item.deleted"} {
		if !contains(actions, want) {
			t.Errorf("the deleted item's history lost %s: %v", want, actions)
		}
	}
	var logs struct {
		Worklogs []struct {
			Body string `json:"body"`
		} `json:"worklogs"`
	}
	f.h.decode(f.mgr.do(http.MethodGet,
		"/api/v1/projects/"+f.pid.String()+"/worklogs?item="+item.ID.String(), nil), http.StatusOK, &logs)
	if len(logs.Worklogs) != 1 {
		t.Errorf("the worklog on a deleted item disappeared: %v", logs.Worklogs)
	}

	// And the ref is never reissued. ZP-1 must name exactly one thing forever,
	// because somebody has written it down somewhere this system cannot see.
	next := f.createItem(f.mgr, map[string]any{"title": "The replacement"})
	if next.Ref == item.Ref {
		t.Errorf("ref %s was reissued after the item holding it was deleted", next.Ref)
	}
}

// TestTimeInStatusIsOneQueryOverTheAuditLog is the eighth exit criterion.
//
// The point is not the number it produces. It is that the number exists at all
// without a stored duration anywhere — no time_in_status, no blocked_since. A
// stored duration is a second source of truth that disagrees with the log under
// exactly the conditions nobody tests.
func TestTimeInStatusIsOneQueryOverTheAuditLog(t *testing.T) {
	f := newPlanFixture(t)
	item := f.createItem(f.mgr, map[string]any{
		"title": "Measured", "assignee_id": f.dev.userID.String(),
	})
	for _, status := range []string{"in_progress", "blocked", "in_progress", "done"} {
		f.h.decode(f.patchItem(f.dev, item.ID, map[string]any{"status": status}), http.StatusOK, nil)
		time.Sleep(5 * time.Millisecond)
	}

	// One query. The initial status comes from work_item.created, every move
	// from work_item.status_changed, and the interval from a window function.
	const timeInStatus = `
		WITH events AS (
		  SELECT after->>'status' AS status, at
		    FROM audit_log
		   WHERE subject_id = $1
		     AND action IN ('work_item.created', 'work_item.status_changed')
		), spans AS (
		  SELECT status, at, lead(at) OVER (ORDER BY at) AS next_at FROM events
		)
		SELECT status, sum(coalesce(next_at, now()) - at) AS total
		  FROM spans GROUP BY status ORDER BY status`

	rows, err := f.h.db.Pool.Query(context.Background(), timeInStatus, item.ID)
	if err != nil {
		t.Fatalf("the time-in-status query does not run: %v", err)
	}
	defer rows.Close()

	seen := map[string]time.Duration{}
	for rows.Next() {
		var (
			status string
			total  time.Duration
		)
		if err := rows.Scan(&status, &total); err != nil {
			t.Fatal(err)
		}
		seen[status] = total
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"todo", "in_progress", "blocked", "done"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("the query cannot see time spent in %q: %v", want, seen)
		}
	}
	// Visited twice, so the two spans must have been summed rather than the
	// second overwriting the first.
	if seen["in_progress"] <= seen["blocked"] {
		t.Errorf("in_progress was visited twice and should total more than blocked: %v", seen)
	}

	// No stored duration exists anywhere in the plan.
	var stored int
	err = f.h.db.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND table_name IN ('milestones','work_items','sub_tasks','checklist_items','deviations','worklogs')
		   AND (column_name LIKE '%%duration%%' OR column_name LIKE '%%time_in%%' OR column_name LIKE '%%_since')`).
		Scan(&stored)
	if err != nil {
		t.Fatal(err)
	}
	if stored != 0 {
		t.Errorf("%d stored duration column(s) exist in the plan; given complete transitions they are a second "+
			"source of truth that will disagree with the log", stored)
	}
}

// ── the rules the criteria depend on ─────────────────────────────────────────

// TestAnyoneOnTheProjectCanRecordWhatHappened is D42.
//
// Worklogs, deviations and notes are dev-owned and project-scoped, which is a
// floor rather than a ceiling. Read as a ceiling it means a manager cannot file
// a worklog on their own project — and a manager who cannot record what went
// wrong stops recording it, which is precisely the evidence a retrospective is
// built from.
func TestAnyoneOnTheProjectCanRecordWhatHappened(t *testing.T) {
	f := newPlanFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Shared", "assignee_id": f.dev.userID.String()})

	for _, c := range []*client{f.mgr, f.dev, f.admin} {
		f.h.decode(c.do(http.MethodPost, "/api/v1/work-items/"+item.ID.String()+"/worklogs",
			map[string]any{"kind": "note", "body": "an observation"}), http.StatusCreated, nil)
		f.h.decode(c.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/deviations",
			map[string]any{"summary": "reality diverged", "severity": "low"}), http.StatusCreated, nil)
	}

	// There is no way to edit or delete a worklog, and the refusal explains the
	// correction route rather than surfacing as a missing route.
	var logs struct {
		Worklogs []struct {
			ID uuid.UUID `json:"id"`
		} `json:"worklogs"`
	}
	f.h.decode(f.mgr.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/worklogs", nil),
		http.StatusOK, &logs)
	if len(logs.Worklogs) != 3 {
		t.Fatalf("expected three worklogs, got %d", len(logs.Worklogs))
	}
	resp := f.mgr.do(http.MethodDelete, "/api/v1/worklogs/"+logs.Worklogs[0].ID.String(), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a worklog delete route exists (HTTP %d); worklogs are corrected by adding another", resp.StatusCode)
	}
}

// TestDeviationOwnershipIsSplit covers the one row of §4.2 that is split
// between two roles: the reporter saw it, the manager decides what to do.
func TestDeviationOwnershipIsSplit(t *testing.T) {
	f := newPlanFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "Auth", "assignee_id": f.dev.userID.String()})

	var dev struct {
		ID       uuid.UUID `json:"id"`
		Resolved bool      `json:"resolved"`
		ItemRef  string    `json:"item_ref"`
	}
	f.h.decode(f.dev.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/deviations", map[string]any{
		"work_item_id": item.ID.String(),
		"summary":      "the token store cannot do what we assumed",
		"severity":     "high",
		"body":         "tried it on Tuesday; it does not support the query we planned around",
	}), http.StatusCreated, &dev)
	if dev.ItemRef != item.Ref {
		t.Errorf("the deviation does not name its item: %q", dev.ItemRef)
	}

	// The worklog was filed alongside it and linked.
	var logs struct {
		Worklogs []struct {
			Kind string `json:"kind"`
		} `json:"worklogs"`
	}
	f.h.decode(f.dev.do(http.MethodGet, "/api/v1/projects/"+f.pid.String()+"/worklogs?kind=deviation", nil),
		http.StatusOK, &logs)
	if len(logs.Worklogs) != 1 {
		t.Errorf("supplying a body should file the worklog describing the deviation: %v", logs.Worklogs)
	}

	// The reporter owns severity; a manager does not.
	f.h.decode(f.dev.do(http.MethodPatch, "/api/v1/deviations/"+dev.ID.String(),
		map[string]any{"severity": "medium"}), http.StatusOK, nil)
	f.h.decodeErr(f.mgr.do(http.MethodPatch, "/api/v1/deviations/"+dev.ID.String(),
		map[string]any{"severity": "low"}), http.StatusForbidden)

	// The manager owns the resolution; the reporter does not.
	f.h.decodeErr(f.dev.do(http.MethodPatch, "/api/v1/deviations/"+dev.ID.String(),
		map[string]any{"resolution": "we will use a different store"}), http.StatusForbidden)

	var resolved struct {
		Resolved   bool   `json:"resolved"`
		ResolvedAt string `json:"resolved_at"`
	}
	f.h.decode(f.mgr.do(http.MethodPatch, "/api/v1/deviations/"+dev.ID.String(),
		map[string]any{"resolution": "we will use a different store"}), http.StatusOK, &resolved)
	if !resolved.Resolved || resolved.ResolvedAt == "" {
		t.Errorf("setting a resolution should resolve the deviation: %+v", resolved)
	}
	if !contains(f.auditActions(dev.ID), "deviation.resolved") {
		t.Errorf("resolving was not recorded as a transition: %v", f.auditActions(dev.ID))
	}

	// Reopening is the same transition read backwards, and at least as
	// interesting: something judged settled turned out not to be.
	//
	// Decoded into a fresh value on purpose: resolved_at is omitempty, so
	// reusing the one above would leave the old timestamp in place and the
	// assertion would pass on a field the response never sent.
	var reopenedView struct {
		Resolved   bool   `json:"resolved"`
		ResolvedAt string `json:"resolved_at"`
	}
	f.h.decode(f.mgr.do(http.MethodPatch, "/api/v1/deviations/"+dev.ID.String(),
		map[string]any{"resolution": ""}), http.StatusOK, &reopenedView)
	if reopenedView.Resolved || reopenedView.ResolvedAt != "" {
		t.Errorf("clearing the resolution should reopen it: %+v", reopenedView)
	}
	reopened := 0
	for _, a := range f.auditActions(dev.ID) {
		if a == "deviation.resolved" {
			reopened++
		}
	}
	if reopened != 2 {
		t.Errorf("deviation.resolved recorded %d times; the reopen is a transition too", reopened)
	}
}

// TestClosedVocabulariesTeach: a rejected status lists every valid value with
// its meaning. That text is read by the planner agent in Phase 6 as well as by
// a human, and a bare refusal produces a second invalid value.
func TestClosedVocabulariesTeach(t *testing.T) {
	f := newPlanFixture(t)
	item := f.createItem(f.mgr, map[string]any{"title": "V", "assignee_id": f.dev.userID.String()})

	// huma rejects this against the enum before any of our code runs, and its
	// own message is "validation failed" with the values buried in a nested
	// array. The path is what tells the rewrite which `status` this is — a
	// milestone's coarser set, not a work item's.
	body := f.h.decodeErr(f.mgr.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/milestones",
		map[string]any{"name": "M", "status": "in_review"}), http.StatusBadRequest)
	if !strings.Contains(body.Error.Message, "planned") || !strings.Contains(body.Error.Message, "dropped") {
		t.Errorf("the refusal should list the valid milestone statuses: %q", body.Error.Message)
	}

	// A date that is not a date says how to write one.
	body = f.h.decodeErr(f.patchItem(f.mgr, item.ID, map[string]any{"due_date": "next tuesday"}),
		http.StatusBadRequest)
	if !strings.Contains(body.Error.Message, "YYYY-MM-DD") {
		t.Errorf("the refusal should say what a date looks like: %q", body.Error.Message)
	}

	// A range that runs backwards is refused rather than quietly reordered:
	// one of the two dates is a typo and the system cannot tell which.
	body = f.h.decodeErr(f.patchItem(f.mgr, item.ID,
		map[string]any{"start_date": "2026-09-01", "due_date": "2026-08-01"}), http.StatusBadRequest)
	if !strings.Contains(body.Error.Message, "typo") {
		t.Errorf("the refusal should name the ambiguity rather than fixing it: %q", body.Error.Message)
	}
}

// TestAssigningWorkToANonMemberIsRefused: the foreign key permits it and the
// plan does not. Otherwise the assignee discovers it by opening the item and
// getting a 404.
func TestAssigningWorkToANonMemberIsRefused(t *testing.T) {
	f := newPlanFixture(t)
	outsider := f.admin.createUser("outsider@plan.test", "Outsider", policy.RoleDeveloper)

	body := f.h.decodeErr(f.mgr.do(http.MethodPost, "/api/v1/projects/"+f.pid.String()+"/work-items",
		map[string]any{"title": "X", "assignee_id": outsider.userID.String()}), http.StatusBadRequest)
	if !strings.Contains(body.Error.Message, "not a member") {
		t.Errorf("the refusal should say why: %q", body.Error.Message)
	}
}
