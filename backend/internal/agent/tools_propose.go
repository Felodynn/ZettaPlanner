package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/governance"
)

// Propose tools: the only route to anything the requesting user does not own.
//
// A proposal is not a softer write. It is a different object with a different
// lifecycle: it carries a rationale, it is decided by somebody with the
// authority, and it is applied atomically as one revision or not at all (§5.1).
// The agent's job here is to say clearly what it would change and why — the
// rationale is what a human actually reads before deciding, and a proposal
// without a good one wastes the reviewer's time more than it saves.

func proposeTools() []Tool {
	return []Tool{
		{
			Name: "propose_plan_change",
			Kind: KindPropose,
			Description: "Raise a proposal for changes to the plan: milestones, work items, subtasks, and every " +
				"manager-owned field on them. This is the only route to a title, an intent, acceptance criteria, " +
				"a priority, a date, an assignee, a milestone, dependencies or entity refs.",
			Params: schema(`{"type":"object","properties":{
				"title":{"type":"string","description":"One line saying what this would do."},
				"rationale":{"type":"string","description":"Why. This is what the reviewer reads before deciding; say what you know and what you are assuming."},
				"changes":{"type":"array","minItems":1,"items":{"type":"object","properties":{
				  "op":{"type":"string","enum":["create","update","delete"]},
				  "target":{"type":"string","enum":["milestone","work_item","sub_task"]},
				  "ref":{"type":"string","description":"For update and delete: the work item ref (ZP-14) or the milestone name."},
				  "after":{"type":"object","description":"The fields this would set, by their contract names: title, intent, acceptance_criteria, priority, start_date, due_date, assignee_id, milestone_id, status, seq."}},
				  "required":["op","target"]}}},
				"required":["title","rationale","changes"]}`),
			Run: runProposePlanChange,
		},
		{
			Name: "propose_diagram_change",
			Kind: KindPropose,
			Description: "Raise a proposal for changes to the architecture graph. Node kinds and edge relations are a " +
				"closed vocabulary — kinds: system, service, component, datastore, queue, external_system, actor, api, " +
				"job, boundary, note; relations: calls, depends_on, reads_from, writes_to, publishes_to, subscribes_to, " +
				"contains, deploys_to, data_flow. Use data_flow when nothing more specific is true rather than inventing one.",
			Params: schema(`{"type":"object","properties":{
				"title":{"type":"string"},
				"rationale":{"type":"string"},
				"diagram":{"type":"string","description":"The diagram slug these changes apply to."},
				"changes":{"type":"array","minItems":1,"items":{"type":"object","properties":{
				  "op":{"type":"string","enum":["create","update","delete"]},
				  "target":{"type":"string","enum":["diagram_node","diagram_edge","diagram"]},
				  "after":{"type":"object","description":"For a node: slug, kind, name, summary. For an edge: from, to, relation, label — from and to are entity slugs."}},
				  "required":["op","target","after"]}}},
				"required":["title","rationale","diagram","changes"]}`),
			Run: runProposeDiagramChange,
		},
		{
			Name: "add_note",
			Kind: KindPropose,
			Description: "Raise a clarification the harness or a human should resolve: an overlap between two things " +
				"that may be the same, a conflict, something missing. A note is how you say 'these two may be the same " +
				"datastore' without deciding it — deciding it is an architectural judgement and not yours to make.",
			Params: schema(`{"type":"object","properties":{
				"kind":{"type":"string","enum":["overlap","conflict","missing","clarification"]},
				"subject":{"type":"string","description":"An entity slug or a work item ref this is about. Optional."},
				"body":{"type":"string"}},
				"required":["kind","body"]}`),
			Run: runAddNote,
		},
	}
}

func runProposePlanChange(ctx context.Context, s *session, a args) (Result, error) {
	title, err := a.required("title")
	if err != nil {
		return Result{}, err
	}
	rationale, err := a.required("rationale")
	if err != nil {
		return Result{}, err
	}
	raw := a.objs("changes")
	if len(raw) == 0 {
		return Result{}, refuse("a proposal with no changes proposes nothing. Send `changes` as an array of " +
			"{op, target, ref, after}, or say in the conversation that nothing should change.")
	}

	changes := make([]pr.Change, 0, len(raw))
	for i, c := range raw {
		target := pr.ChangeTarget(c.str("target"))
		if err := pr.ValidateChangeTarget(target); err != nil {
			return Result{}, refuse("%s", err.Error())
		}
		if target.IsDiagram() {
			return Result{}, refuse("change %d targets `%s`, which lives in the architecture graph. "+
				"Use `propose_diagram_change` for that — a plan proposal and a diagram proposal are decided by "+
				"different people looking at different things.", i+1, target)
		}
		op := pr.Op(c.str("op"))
		if !op.Valid() {
			return Result{}, refuse("change %d has op %q; the ops are create, update, delete.", i+1, c.str("op"))
		}

		change := pr.Change{Op: op, Target: target, After: c.obj("after")}
		if err := validatePlanChange(target, op, change.After, i); err != nil {
			return Result{}, err
		}
		if ref := c.str("ref"); ref != "" {
			id, before, err := s.resolvePlanTarget(ctx, target, ref)
			if err != nil {
				return Result{}, err
			}
			change.TargetID = &id
			change.Before = before
		}
		if change.TargetID == nil && (op != pr.OpCreate || target == pr.ChangeSubTask) {
			what := "which " + string(target) + " it changes"
			if target == pr.ChangeSubTask {
				what = "which work item it goes under"
			}
			return Result{}, refuse("change %d has no `ref`; say %s.", i+1, what)
		}
		changes = append(changes, change)
	}
	return s.raiseProposal(ctx, title, rationale, changes)
}

// validatePlanChange rejects a field the applier could not set, and a subtask
// change that has nothing to change.
//
// Enforced here as well as at approve time, and the earlier one is the one that
// matters: a proposal is read and agreed to by a person, and discovering after
// they clicked approve that `owner` is not a field wastes exactly the resource
// the whole system is trying to save. The list comes from §4.2 through the
// ownership table, so it cannot drift from what will actually apply.
func validatePlanChange(target pr.ChangeTarget, op pr.Op, after map[string]any, i int) error {
	if target == pr.ChangeSubTask && op != pr.OpCreate {
		return refuse("change %d would %s a subtask. A subtask has no manager-owned field (§4.2), so there is "+
			"nothing on an existing one for a proposal to decide — its assignee edits it directly. "+
			"Propose creating one, or say what should change in the conversation.", i+1, op)
	}
	if op == pr.OpDelete || len(after) == 0 {
		return nil
	}

	settable := governance.SettableFields(target)
	known := make(map[string]bool, len(settable))
	for _, f := range settable {
		known[f] = true
	}
	var unknown []string
	for key := range after {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return refuse("change %d sets %s on a %s, which %s not a field of one. The fields you can set are: %s.",
		i+1, strings.Join(quoted(unknown), ", "), target, isAre(len(unknown)), strings.Join(settable, ", "))
}

func quoted(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = "`" + s + "`"
	}
	return out
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func runProposeDiagramChange(ctx context.Context, s *session, a args) (Result, error) {
	title, err := a.required("title")
	if err != nil {
		return Result{}, err
	}
	rationale, err := a.required("rationale")
	if err != nil {
		return Result{}, err
	}
	slug, err := a.required("diagram")
	if err != nil {
		return Result{}, err
	}
	diagram, err := s.deps.Diagrams.BySlug(ctx, s.turn.ProjectID, slug, s.principal)
	if err != nil {
		if isNotFound(err) {
			return Result{}, refuse("there is no diagram %q in this project. Call `get_diagram` on one that exists, "+
				"or propose creating this one with target `diagram`.", slug)
		}
		return Result{}, err
	}

	raw := a.objs("changes")
	if len(raw) == 0 {
		return Result{}, refuse("a proposal with no changes proposes nothing.")
	}

	// The graph as it stands, read once. Every update and delete records what it
	// found here as `before`, which is what lets the approval step notice that
	// the premise has gone (§5.1). Without it a proposal to re-kind a box would
	// still apply after somebody had already re-kinded it differently, silently
	// reverting their decision.
	view, err := s.deps.Diagrams.Get(ctx, diagram.ID, s.principal)
	if err != nil {
		return Result{}, err
	}

	changes := make([]pr.Change, 0, len(raw))
	for i, c := range raw {
		target := pr.ChangeTarget(c.str("target"))
		if err := pr.ValidateChangeTarget(target); err != nil {
			return Result{}, refuse("%s", err.Error())
		}
		if !target.IsDiagram() {
			return Result{}, refuse("change %d targets `%s`, which is part of the plan. Use `propose_plan_change`.", i+1, target)
		}
		op := pr.Op(c.str("op"))
		if !op.Valid() {
			return Result{}, refuse("change %d has op %q; the ops are create, update, delete.", i+1, c.str("op"))
		}

		after := c.obj("after")
		if err := validateDiagramChange(target, after); err != nil {
			return Result{}, refuse("change %d: %s", i+1, err.Error())
		}
		// Every diagram change names its diagram, so Phase 7 applies it without
		// having to infer which picture a bare node slug belonged to.
		if after == nil {
			after = map[string]any{}
		}
		after["diagram"] = diagram.Slug

		before, err := graphBefore(view.Graph, diagram, target, op, after, i)
		if err != nil {
			return Result{}, err
		}

		id := diagram.ID
		changes = append(changes, pr.Change{Op: op, Target: target, TargetID: &id, After: after, Before: before})
	}
	return s.raiseProposal(ctx, title, rationale, changes)
}

// graphBefore records the state a diagram change is raised against.
//
// A create has no premise about an existing element, so it records nothing. An
// update or a delete that names something the diagram does not contain is
// refused **here**, while the model can still fix it, rather than becoming a
// proposal a human reads, agrees with, and cannot apply.
func graphBefore(
	g *dg.Graph, d *dg.Diagram, target pr.ChangeTarget, op pr.Op, after map[string]any, i int,
) (map[string]any, error) {
	if op == pr.OpCreate {
		return nil, nil
	}
	get := func(key string) string {
		if v, ok := after[key]; ok && v != nil {
			return strings.TrimSpace(fmt.Sprint(v))
		}
		return ""
	}

	switch target {
	case pr.ChangeDiagram:
		return map[string]any{"slug": d.Slug, "name": d.Name, "level": string(d.Level)}, nil

	case pr.ChangeNode:
		slug := get("slug")
		if slug == "" {
			slug = dg.Slugify(get("name"))
		}
		n := diagramsvc.FindNode(g, slug)
		if n == nil {
			return nil, refuse("change %d names the node `%s`, which is not in this diagram. "+
				"Call `get_diagram` to see what is, or propose creating it with op `create`.", i+1, slug)
		}
		return diagramsvc.NodeSnapshot(n), nil

	case pr.ChangeEdge:
		from, to, rel := get("from"), get("to"), dg.Relation(get("relation"))
		e := diagramsvc.FindEdge(g, from, to, rel)
		if e == nil {
			return nil, refuse("change %d names the arrow `%s %s %s`, which is not in this diagram. "+
				"Call `get_diagram` to see what is, or propose creating it with op `create`.", i+1, from, rel, to)
		}
		return diagramsvc.EdgeSnapshot(g, e), nil
	}
	return nil, nil
}

// validateDiagramChange enforces the closed vocabulary at the moment of
// proposing rather than at the moment of applying.
//
// Phase 7 would catch it too, but by then a human has read the proposal and
// approved it, and discovering that `caches_to` is not a relation *after*
// somebody agreed to it wastes the one resource the whole system is trying to
// save.
func validateDiagramChange(target pr.ChangeTarget, after map[string]any) error {
	if after == nil {
		return nil
	}
	get := func(key string) string {
		if v, ok := after[key]; ok && v != nil {
			return strings.TrimSpace(fmt.Sprint(v))
		}
		return ""
	}

	switch target {
	case pr.ChangeNode:
		if kind := get("kind"); kind != "" {
			if err := dg.ValidateNodeKind(dg.NodeKind(kind)); err != nil {
				return err
			}
		}
		if get("slug") == "" && get("name") == "" {
			return fmt.Errorf("a node change needs a `slug` or a `name` in `after`")
		}
	case pr.ChangeEdge:
		if rel := get("relation"); rel != "" {
			if err := dg.ValidateRelation(dg.Relation(rel)); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("an edge change needs a `relation` in `after`")
		}
		if get("from") == "" || get("to") == "" {
			return fmt.Errorf("an edge change needs `from` and `to` in `after`, as entity slugs")
		}
	case pr.ChangeDiagram:
		if level := get("level"); level != "" {
			if err := dg.ValidateLevel(dg.Level(level)); err != nil {
				return err
			}
		}
	}
	return nil
}

func runAddNote(ctx context.Context, s *session, a args) (Result, error) {
	kind := dg.NoteKind(a.str("kind"))
	if !kind.Valid() {
		return Result{}, refuse("%q is not a note kind. They are overlap, conflict, missing, clarification.", a.str("kind"))
	}
	body, err := a.required("body")
	if err != nil {
		return Result{}, err
	}

	note := &dg.Note{
		ID:         uuid.Must(uuid.NewV7()),
		ProjectID:  s.turn.ProjectID,
		Kind:       kind,
		Body:       body,
		Status:     dg.NoteOpen,
		Provenance: dg.ProvAgentProposed,
		ActorID:    s.principal.WritingActorID(),
		CreatedAt:  time.Now(),
	}
	if subject := a.str("subject"); subject != "" {
		kindName, id, err := s.resolveNoteSubject(ctx, subject)
		if err != nil {
			return Result{}, err
		}
		note.SubjectKind, note.SubjectID = kindName, &id
	} else {
		note.SubjectKind = "project"
		id := s.turn.ProjectID
		note.SubjectID = &id
	}

	err = s.deps.DB.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.deps.notes.Create(ctx, tx, note); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &note.ProjectID,
			ActorID:    s.principal.WritingActorID(),
			OnBehalfOf: s.principal.OnBehalfOf(),
			Action:     audit.ActionNoteCreated,
			SubjectID:  &note.ID,
			After: map[string]any{
				"kind": string(note.Kind), "subject_kind": note.SubjectKind, "body": note.Body,
			},
		})
	})
	if err != nil {
		return Result{}, err
	}
	return Result{
		Produced: true,
		Content: fmt.Sprintf("Note raised (%s). It is open and will appear in this project's context until "+
			"somebody resolves or dismisses it. You have not decided anything by raising it.", note.Kind),
	}, nil
}
