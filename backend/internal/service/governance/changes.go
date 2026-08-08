package governance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
)

// Turning one recorded change into one typed service call.
//
// The mapping is strict in one direction: **an `after` key this file does not
// recognise is a refusal, never a silent skip.** A proposal is read and agreed
// to by a person, and quietly dropping the half of it the applier did not
// understand delivers something nobody approved while reporting success. The
// error names the field and lists what is settable, which is the same teaching
// shape every closed vocabulary in this system uses.

// applyPlanChange runs one milestone, work item or subtask change.
func (s *Service) applyPlanChange(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, c pr.Change, w plansvc.Writer) error {
	switch c.Target {
	case pr.ChangeMilestone:
		return s.applyMilestone(ctx, tx, projectID, c, w)
	case pr.ChangeWorkItem:
		return s.applyWorkItem(ctx, tx, projectID, c, w)
	case pr.ChangeSubTask:
		return s.applySubTask(ctx, tx, c, w)
	}
	return unapplicable(c, fmt.Sprintf("targets %q, which this version cannot apply", c.Target))
}

// SettableFields is what a proposal may set on one target, taken from the
// ownership table rather than listed here.
//
// §4.2 is the enforcement mechanism, and a second hand-written list beside it
// would drift: adding a field to the table would make it *proposable* and leave
// it unappliable, which is discovered at the worst moment — after somebody has
// read a proposal and agreed to it. A generated test asserts that every field
// this returns actually reaches a service call.
func SettableFields(target pr.ChangeTarget) []string {
	kind, ok := objectKindFor(target)
	if !ok || target.IsDiagram() {
		return nil
	}
	fields, all, known := policy.Rules(kind)
	if !known || all != nil {
		return nil
	}
	out := make([]string, 0, len(fields))
	for f := range fields {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

var milestoneFields = SettableFields(pr.ChangeMilestone)

func (s *Service) applyMilestone(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, c pr.Change, w plansvc.Writer) error {
	if c.Op == pr.OpDelete {
		return s.plan.DeleteMilestoneInTx(ctx, tx, *c.TargetID, w)
	}

	f := reader{change: c, allowed: milestoneFields, object: "milestone"}
	name, intent := f.strPtr("name"), f.strPtr("intent")
	start, due := f.strPtr("start_date"), f.strPtr("due_date")
	status, seq := f.strPtr("status"), f.intPtr("seq")
	if err := f.err(); err != nil {
		return err
	}

	if c.Op == pr.OpCreate {
		_, err := s.plan.CreateMilestoneInTx(ctx, tx, plansvc.CreateMilestoneInput{
			ProjectID: projectID,
			Name:      deref(name), Intent: deref(intent),
			StartDate: deref(start), DueDate: deref(due), Status: deref(status),
			Seq: seq,
		}, w)
		return err
	}

	// The field list is what the ownership check is given, so it is derived
	// from what was read rather than declared alongside it. Two lists would
	// eventually disagree, and the one that decides permission is the one that
	// would be wrong.
	fields := f.fields()
	if len(fields) == 0 {
		return nil
	}
	_, err := s.plan.UpdateMilestoneInTx(ctx, tx, *c.TargetID, plansvc.UpdateMilestoneInput{
		Fields: fields, Name: name, Intent: intent,
		StartDate: start, DueDate: due, Status: status, Seq: seq,
	}, w)
	return err
}

var workItemFields = SettableFields(pr.ChangeWorkItem)

func (s *Service) applyWorkItem(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, c pr.Change, w plansvc.Writer) error {
	if c.Op == pr.OpDelete {
		return s.plan.DeleteWorkItemInTx(ctx, tx, *c.TargetID, w)
	}

	f := reader{change: c, allowed: workItemFields, object: "work item"}
	title, intent := f.strPtr("title"), f.strPtr("intent")
	acceptance, status := f.strPtr("acceptance_criteria"), f.strPtr("status")
	progress, priority := f.intPtr("progress_pct"), f.strPtr("priority")
	assignee, milestone := f.strPtr("assignee_id"), f.strPtr("milestone_id")
	start, due := f.strPtr("start_date"), f.strPtr("due_date")
	// Links are set through their own endpoints even on the direct path, so they
	// are read here and applied after the item exists rather than folded into
	// the patch.
	blocks, relates, hasDeps := f.deps()
	refSlugs, hasRefs := f.list("entity_refs")
	if err := f.err(); err != nil {
		return err
	}

	target := c.TargetID
	if c.Op == pr.OpCreate {
		created, err := s.plan.CreateWorkItemInTx(ctx, tx, plansvc.CreateWorkItemInput{
			ProjectID: projectID,
			Title:     deref(title), Intent: deref(intent), Acceptance: deref(acceptance),
			Status: deref(status), Priority: deref(priority),
			AssigneeID: deref(assignee), MilestoneID: deref(milestone),
			StartDate: deref(start), DueDate: deref(due),
		}, w)
		if err != nil {
			return err
		}
		target = &created.ID
	} else if fields := withoutLinks(f.fields()); len(fields) > 0 {
		if _, err := s.plan.UpdateWorkItemInTx(ctx, tx, *target, plansvc.UpdateWorkItemInput{
			Fields: fields, Title: title, Intent: intent, Acceptance: acceptance,
			Status: status, ProgressPct: progress, Priority: priority,
			AssigneeID: assignee, MilestoneID: milestone, StartDate: start, DueDate: due,
		}, w); err != nil {
			return err
		}
	}

	if hasDeps {
		in := plansvc.SetDependenciesInput{}
		if blocks != nil {
			ids, err := s.itemIDsForRefs(ctx, tx, projectID, blocks)
			if err != nil {
				return err
			}
			in.Blocks = &ids
		}
		if relates != nil {
			ids, err := s.itemIDsForRefs(ctx, tx, projectID, relates)
			if err != nil {
				return err
			}
			in.Relates = &ids
		}
		if _, err := s.plan.SetDependenciesInTx(ctx, tx, *target, in, w); err != nil {
			return err
		}
	}
	if hasRefs {
		ids, err := s.plan.EntityIDsForSlugs(ctx, tx, projectID, refSlugs)
		if err != nil {
			return err
		}
		if _, err := s.plan.SetEntityRefsInTx(ctx, tx, *target, ids, w); err != nil {
			return err
		}
	}
	return nil
}

// withoutLinks drops the two fields that are written through their own calls.
//
// They stay in the ownership check that those calls make — `dependencies` and
// `entity_refs` are manager-owned in §4.2 and CheckWrite runs on both paths.
// What they must not do is reach the item PATCH, which would record an edit
// that changed nothing.
func withoutLinks(fields []string) []string {
	out := fields[:0:0]
	for _, f := range fields {
		if f == "dependencies" || f == "entity_refs" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// itemIDsForRefs resolves the refs a proposal names into work item ids.
//
// Refs rather than ids, because `ZP-14` is what a model has seen and what a
// person reading the proposal recognises. An unknown ref is an error naming it:
// a dependency list that quietly lost an entry is a plan that says something
// nobody proposed.
func (s *Service) itemIDsForRefs(ctx context.Context, tx pgx.Tx, projectID uuid.UUID, refs []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(refs))
	for _, raw := range refs {
		ref := strings.ToUpper(strings.TrimSpace(raw))
		if ref == "" {
			continue
		}
		if id, err := uuid.Parse(raw); err == nil {
			out = append(out, id)
			continue
		}
		item, err := s.items.ByRef(ctx, tx, projectID, ref)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return nil, fmt.Errorf("%w: there is no work item %s in this project", domain.ErrNotFound, ref)
			}
			return nil, err
		}
		out = append(out, item.ID)
	}
	return out, nil
}

var subTaskFields = SettableFields(pr.ChangeSubTask)

// applySubTask creates a subtask under the work item the change names.
//
// Create only, and that is the whole story rather than a gap: §4.2 gives a
// SubTask **no manager-owned fields at all**, so there is nothing on an existing
// one that a proposal could be for. Its assignee changes it directly. The one
// thing needing governance is putting new work under somebody else's item, which
// is a create.
func (s *Service) applySubTask(ctx context.Context, tx pgx.Tx, c pr.Change, w plansvc.Writer) error {
	if c.Op != pr.OpCreate {
		return unapplicable(c, "a subtask has no manager-owned field (§4.2), so there is nothing on an existing "+
			"one for a proposal to change. Its assignee edits it directly")
	}
	if c.TargetID == nil {
		return unapplicable(c, "names no work item to add the subtask to")
	}

	f := reader{change: c, allowed: subTaskFields, object: "subtask"}
	title, status := f.strPtr("title"), f.strPtr("status")
	assignee, seq := f.strPtr("assignee_id"), f.intPtr("seq")
	if err := f.err(); err != nil {
		return err
	}
	_, err := s.plan.CreateSubTaskInTx(ctx, tx, plansvc.CreateSubTaskInput{
		WorkItemID: *c.TargetID,
		Title:      deref(title), Status: deref(status), AssigneeID: deref(assignee),
		Seq: seq,
	}, w)
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ── the reader ───────────────────────────────────────────────────────────────

// reader pulls typed values out of a change's `after`, collecting failures
// rather than returning at the first one.
//
// Collecting matters here: a proposal with three unrecognised fields should say
// so once, not be resubmitted three times. It is also what makes the "unknown
// field" check total — every key is visited whether or not an earlier one
// failed.
type reader struct {
	change  pr.Change
	allowed []string
	object  string

	touched []string
	failed  []string
}

func (r *reader) value(key string) (any, bool) {
	v, ok := r.change.After[key]
	if !ok {
		return nil, false
	}
	r.touched = append(r.touched, key)
	return v, true
}

func (r *reader) str(key string) string {
	v, ok := r.value(key)
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func (r *reader) strPtr(key string) *string {
	if _, ok := r.change.After[key]; !ok {
		return nil
	}
	s := r.str(key)
	return &s
}

func (r *reader) intPtr(key string) *int {
	v, ok := r.value(key)
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case float64:
		n := int(t)
		return &n
	case int:
		return &t
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			r.failed = append(r.failed, fmt.Sprintf("%s is %q, which is not a number", key, t))
			return nil
		}
		return &n
	}
	r.failed = append(r.failed, fmt.Sprintf("%s is not a number", key))
	return nil
}

// list reads an array of strings, which is how a proposal names entity slugs
// and work item refs.
func (r *reader) list(key string) ([]string, bool) {
	v, ok := r.value(key)
	if !ok || v == nil {
		return nil, ok
	}
	raw, isSlice := v.([]any)
	if !isSlice {
		r.failed = append(r.failed, fmt.Sprintf("%s is not a list", key))
		return nil, true
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if item == nil {
			continue
		}
		out = append(out, strings.TrimSpace(fmt.Sprint(item)))
	}
	return out, true
}

// deps reads `dependencies` as {blocks: [...], relates: [...]}.
//
// A nil slice means "leave this kind alone" and an empty one means "clear it",
// which is the same distinction the PUT endpoint makes — sending only `blocks`
// must not silently wipe `relates`.
func (r *reader) deps() (blocks, relates []string, present bool) {
	v, ok := r.value("dependencies")
	if !ok || v == nil {
		return nil, nil, ok
	}
	obj, isObj := v.(map[string]any)
	if !isObj {
		r.failed = append(r.failed, "dependencies is not an object; send {\"blocks\": [refs], \"relates\": [refs]}")
		return nil, nil, true
	}
	read := func(key string) []string {
		raw, ok := obj[key]
		if !ok || raw == nil {
			return nil
		}
		items, isSlice := raw.([]any)
		if !isSlice {
			r.failed = append(r.failed, fmt.Sprintf("dependencies.%s is not a list of work item refs", key))
			return nil
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, strings.TrimSpace(fmt.Sprint(item)))
		}
		return out
	}
	return read("blocks"), read("relates"), true
}

// fields is the set of §4.2 wire names this change writes, which is what the
// ownership check is given.
func (r *reader) fields() []string {
	out := append([]string(nil), r.touched...)
	sort.Strings(out)
	return out
}

// err reports unknown keys and unreadable values.
func (r *reader) err() error {
	known := map[string]bool{}
	for _, f := range r.allowed {
		known[f] = true
	}
	var unknown []string
	for key := range r.change.After {
		if key == "diagram" || known[key] {
			continue
		}
		unknown = append(unknown, key)
	}
	sort.Strings(unknown)

	if len(unknown) == 0 && len(r.failed) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "change %d cannot be applied. ", r.change.Seq)
	if len(unknown) > 0 {
		fmt.Fprintf(&b, "It sets %s on a %s, which %s not settable — the settable fields are %s. ",
			strings.Join(quoteAll(unknown), ", "), r.object, isAre(len(unknown)), strings.Join(r.allowed, ", "))
	}
	if len(r.failed) > 0 {
		fmt.Fprintf(&b, "%s. ", strings.Join(r.failed, "; "))
	}
	b.WriteString("Nothing was applied: a proposal is agreed to as a whole, and applying the part that " +
		"was understood would deliver something nobody approved.")
	return fmt.Errorf("%w: %s", domain.ErrInvalid, b.String())
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strconv.Quote(s)
	}
	return out
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// ── diagram edits ────────────────────────────────────────────────────────────

// graphEdit turns one recorded change into one element-level edit.
func graphEdit(c pr.Change) (diagramsvc.GraphEdit, error) {
	op := diagramsvc.EditOp(c.Op)

	switch c.Target {
	case pr.ChangeNode:
		slug := strings.TrimSpace(changeString(c, "slug"))
		if slug == "" {
			slug = dg.Slugify(changeString(c, "name"))
		}
		if slug == "" {
			return diagramsvc.GraphEdit{}, unapplicable(c, "names no node; a node is addressed by its entity slug")
		}
		node := diagramsvc.NodeInput{
			Slug:    slug,
			Kind:    dg.NodeKind(strings.TrimSpace(fmt.Sprint(orEmpty(c.After["kind"])))),
			Name:    strings.TrimSpace(fmt.Sprint(orEmpty(c.After["name"]))),
			Summary: strings.TrimSpace(fmt.Sprint(orEmpty(c.After["summary"]))),
			Parent:  strings.TrimSpace(fmt.Sprint(orEmpty(c.After["parent"]))),
		}
		return diagramsvc.GraphEdit{Op: op, Node: &node}, nil

	case pr.ChangeEdge:
		from, to := changeString(c, "from"), changeString(c, "to")
		rel := dg.Relation(changeString(c, "relation"))
		if from == "" || to == "" || rel == "" {
			return diagramsvc.GraphEdit{}, unapplicable(c,
				"an edge is addressed by from, to and relation, and this one is missing at least one")
		}
		edge := diagramsvc.EdgeInput{
			From: from, To: to, Relation: rel,
			Label: strings.TrimSpace(fmt.Sprint(orEmpty(c.After["label"]))),
		}
		return diagramsvc.GraphEdit{Op: op, Edge: &edge}, nil

	case pr.ChangeDiagram:
		if c.Op != pr.OpUpdate {
			return diagramsvc.GraphEdit{}, unapplicable(c,
				"a diagram itself is created and deleted through its own endpoints, not through a proposal")
		}
		return diagramsvc.GraphEdit{Op: op, Diagram: &diagramsvc.DiagramEdit{
			Name:  strings.TrimSpace(fmt.Sprint(orEmpty(c.After["name"]))),
			Level: dg.Level(strings.TrimSpace(fmt.Sprint(orEmpty(c.After["level"])))),
		}}, nil
	}
	return diagramsvc.GraphEdit{}, unapplicable(c, "is not a diagram change")
}

func orEmpty(v any) any {
	if v == nil {
		return ""
	}
	return v
}
