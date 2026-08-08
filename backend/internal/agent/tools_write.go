package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
)

// Write tools: the only direct-write path an agent has.
//
// Every one of them is confined to a **dev-owned** field on something belonging
// to the person who asked for the turn (§4.2, D11). There is no tool here that
// sets a due date, an assignee, a priority or a milestone, and no combination
// of these reaches one — an agent's route to a manager-owned field is a
// proposal, and the inventory is what makes that true rather than the prompt.
//
// The check is not in this file either. `update_dev_field` hands the field name
// to the plan service, which resolves it through the ownership table with the
// requesting user's assignment; a field that is not dev-owned is refused there,
// by the same code that refuses a human.

func writeTools() []Tool {
	return []Tool{
		{
			Name:   "update_dev_field",
			Kind:   KindWrite,
			Object: policy.KindWorkItem,
			Description: "Write a developer-owned field on a work item assigned to the person who asked for this turn. " +
				"Only `status` and `progress_pct` are developer-owned; everything else about an item is manager-owned " +
				"and goes through `propose_plan_change`.",
			Params: schema(`{"type":"object","properties":{
				"ref":{"type":"string","description":"The work item ref, such as ZP-14."},
				"field":{"type":"string","enum":["status","progress_pct"],
				         "description":"status: todo|in_progress|blocked|review|done|dropped. progress_pct: 0-100."},
				"value":{"type":"string","description":"The new value, as a string."}},
				"required":["ref","field","value"]}`),
			Run: runUpdateDevField,
		},
		{
			Name:   "add_worklog",
			Kind:   KindWrite,
			Object: policy.KindWorklog,
			Description: "Append a worklog. Anyone on the project may write one and they are never edited — a correction " +
				"is another worklog. Use `progress` for what was done, `deviation` when reality diverged from the plan, " +
				"`note` for context that is neither.",
			Params: schema(`{"type":"object","properties":{
				"ref":{"type":"string","description":"The work item this is about. Omit for a project-level entry."},
				"kind":{"type":"string","enum":["progress","deviation","note"]},
				"body":{"type":"string","description":"What happened, in the words somebody would use to a colleague."}},
				"required":["body"]}`),
			Run: runAddWorklog,
		},
		{
			Name:        "add_subtask",
			Kind:        KindWrite,
			Object:      policy.KindSubTask,
			Description: "Add a subtask under a work item assigned to the person who asked. Subtasks are leaves: there is no nesting below them.",
			Params: schema(`{"type":"object","properties":{
				"ref":{"type":"string"},
				"title":{"type":"string"}},
				"required":["ref","title"]}`),
			Run: runAddSubTask,
		},
		{
			Name:   "check_item",
			Kind:   KindWrite,
			Object: policy.KindChecklistItem,
			Description: "Tick or clear a checklist item on a work item assigned to the person who asked. " +
				"Clearing one is as much a signal as ticking it and is recorded the same way.",
			Params: schema(`{"type":"object","properties":{
				"ref":{"type":"string"},
				"text":{"type":"string","description":"The checklist item's text, or enough of it to identify one."},
				"checked":{"type":"boolean"}},
				"required":["ref","text","checked"]}`),
			Run: runCheckItem,
		},
	}
}

func runUpdateDevField(ctx context.Context, s *session, a args) (Result, error) {
	ref, err := a.required("ref")
	if err != nil {
		return Result{}, err
	}
	field, err := a.required("field")
	if err != nil {
		return Result{}, err
	}
	value, err := a.required("value")
	if err != nil {
		return Result{}, err
	}

	item, err := s.item(ctx, ref)
	if err != nil {
		return Result{}, err
	}

	in := plansvc.UpdateWorkItemInput{Fields: []string{field}}
	switch field {
	case "status":
		if err := pl.ValidateStatus(pl.Status(value)); err != nil {
			return Result{}, refuse("%s", err.Error())
		}
		in.Status = &value
	case "progress_pct":
		n, ok := a.intVal("value")
		if !ok || n < 0 || n > 100 {
			return Result{}, refuse("progress_pct is a percentage from 0 to 100; %q is not one.", value)
		}
		in.ProgressPct = &n
	default:
		// The schema enumerates the two, so arriving here means the model
		// ignored it. Name the boundary and the route rather than the schema.
		return Result{}, denied(
			"`%s` is not a developer-owned field, so no tool can write it directly. The developer-owned fields "+
				"are `status` and `progress_pct`, and only on items assigned to the person who asked. "+
				"Everything else about a work item — title, intent, acceptance criteria, priority, dates, assignee, "+
				"milestone, dependencies, entity refs — is manager-owned: raise it with `propose_plan_change`.", field)
	}

	updated, err := s.deps.Plan.UpdateWorkItem(ctx, item.Item.ID, in, s.principal)
	if err != nil {
		return Result{}, s.translate(err, "propose_plan_change")
	}
	return Result{
		Produced: true,
		Content: fmt.Sprintf("%s.%s is now %s. Written directly with provenance `agent_committed`; "+
			"the audit row names both this agent and %s.",
			updated.Item.Ref, field, value, s.requesterName),
	}, nil
}

func runAddWorklog(ctx context.Context, s *session, a args) (Result, error) {
	body, err := a.required("body")
	if err != nil {
		return Result{}, err
	}
	kind := a.str("kind")
	if kind == "" {
		kind = string(pl.WorklogProgress)
	}
	if err := pl.ValidateWorklogKind(pl.WorklogKind(kind)); err != nil {
		return Result{}, refuse("%s", err.Error())
	}

	var itemID *uuid.UUID
	if ref := a.str("ref"); ref != "" {
		item, err := s.item(ctx, ref)
		if err != nil {
			return Result{}, err
		}
		id := item.Item.ID
		itemID = &id
	}

	w, err := s.deps.Plan.AddWorklog(ctx, itemID, s.turn.ProjectID, kind, body, s.principal)
	if err != nil {
		return Result{}, s.translate(err, "")
	}
	where := "the project"
	if w.ItemRef != "" {
		where = w.ItemRef
	}
	return Result{
		Produced: true,
		Content:  fmt.Sprintf("Worklog (%s) recorded on %s. It is indexed and searchable and will not be edited.", w.Kind, where),
	}, nil
}

func runAddSubTask(ctx context.Context, s *session, a args) (Result, error) {
	ref, err := a.required("ref")
	if err != nil {
		return Result{}, err
	}
	title, err := a.required("title")
	if err != nil {
		return Result{}, err
	}
	item, err := s.item(ctx, ref)
	if err != nil {
		return Result{}, err
	}

	st, err := s.deps.Plan.CreateSubTask(ctx, plansvc.CreateSubTaskInput{
		WorkItemID: item.Item.ID, Title: title,
	}, s.principal)
	if err != nil {
		return Result{}, s.translate(err, "propose_plan_change")
	}
	return Result{Produced: true, Content: fmt.Sprintf("Subtask %q added to %s.", st.Title, item.Item.Ref)}, nil
}

func runCheckItem(ctx context.Context, s *session, a args) (Result, error) {
	ref, err := a.required("ref")
	if err != nil {
		return Result{}, err
	}
	text, err := a.required("text")
	if err != nil {
		return Result{}, err
	}
	checked, ok := a.boolVal("checked")
	if !ok {
		return Result{}, missingArg("checked")
	}

	item, err := s.item(ctx, ref)
	if err != nil {
		return Result{}, err
	}
	if len(item.Checklist) == 0 {
		return Result{}, refuse("%s has no checklist. There is no tool that creates one — a checklist is "+
			"something a person writes for themselves.", item.Item.Ref)
	}

	match := matchChecklist(item.Checklist, text)
	if match == nil {
		var have []string
		for _, c := range item.Checklist {
			have = append(have, strconv.Quote(c.Text))
		}
		return Result{}, refuse("no checklist item on %s matches %q. The items are: %s",
			item.Item.Ref, text, strings.Join(have, ", "))
	}

	updated, err := s.deps.Plan.UpdateChecklistItem(ctx, match.ID,
		plansvc.UpdateChecklistInput{Fields: []string{"checked"}, Checked: &checked}, s.principal)
	if err != nil {
		return Result{}, s.translate(err, "propose_plan_change")
	}
	verb := "cleared"
	if updated.Checked {
		verb = "ticked"
	}
	return Result{Produced: true, Content: fmt.Sprintf("%q on %s is now %s.", updated.Text, item.Item.Ref, verb)}, nil
}

// matchChecklist finds the item a loose piece of text refers to.
//
// Exact first, then a unique prefix, then a unique substring. An ambiguous
// match is not resolved by picking the first: ticking the wrong box on
// somebody's checklist is a small lie that is hard to spot later.
func matchChecklist(list []*pl.ChecklistItem, text string) *pl.ChecklistItem {
	want := strings.ToLower(strings.TrimSpace(text))
	for _, c := range list {
		if strings.EqualFold(strings.TrimSpace(c.Text), want) {
			return c
		}
	}
	var found *pl.ChecklistItem
	for _, c := range list {
		if strings.HasPrefix(strings.ToLower(c.Text), want) {
			if found != nil {
				return nil
			}
			found = c
		}
	}
	if found != nil {
		return found
	}
	for _, c := range list {
		if strings.Contains(strings.ToLower(c.Text), want) {
			if found != nil {
				return nil
			}
			found = c
		}
	}
	return found
}

// isNotFound reports the domain's absence sentinel.
func isNotFound(err error) bool { return errors.Is(err, errNotFound) }
