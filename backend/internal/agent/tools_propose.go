package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
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
				"a priority, a date, an assignee, a milestone, dependencies or entity refs. " +
				"Changes apply in the order you send them, inside one transaction, so a milestone created by change 1 " +
				"can be named by change 2. Everything is addressed the way you read it — a work item by its ref, " +
				"a milestone by its name, a person by their name or email, an entity by its slug. There are no ids " +
				"to look up and none to invent.",
			Params: schema(`{"type":"object","properties":{
				"title":{"type":"string","description":"One line saying what this would do."},
				"rationale":{"type":"string","description":"Why. This is what the reviewer reads before deciding; say what you know and what you are assuming."},
				"changes":{"type":"array","minItems":1,"items":{"type":"object","properties":{
				  "op":{"type":"string","enum":["create","update","delete"],
				        "description":"create makes something new; update changes named fields on something that exists; delete soft-deletes it."},
				  "target":{"type":"string","enum":["milestone","work_item","sub_task"]},
				  "ref":{"type":"string","description":"Which existing thing this change is about: a work item ref (ZP-14) or a milestone name. Required for update and delete. For a sub_task create it is the ref of the work item the subtask goes under. Omit it when creating a milestone or a work item."},
				  "after":{"type":"object","description":"The fields this change sets. A milestone takes name, intent, status, start_date, due_date, seq. A work item takes title, intent, acceptance_criteria, priority, status, progress_pct, milestone_id, assignee_id, start_date, due_date, dependencies, entity_refs. A sub_task takes title, status, assignee_id, seq. Sending a field the target does not have is refused, not ignored.",
				    "properties":{
				      "title":{"type":"string","description":"Work item or subtask. Required when creating one."},
				      "name":{"type":"string","description":"Milestone only. Required when creating one."},
				      "intent":{"type":"string","description":"What this is for, in prose."},
				      "acceptance_criteria":{"type":"string","description":"Work item only: what has to be true for it to be done."},
				      "priority":{"type":"string","enum":["low","normal","high","urgent"],
				                  "description":"Work item only, and a closed set: low, normal, high, urgent. There is no P0/P1/P2 here. Leave it out rather than inventing a level — unset means nobody has triaged it, which is itself worth knowing."},
				      "status":{"type":"string",
				                "description":"A work item or subtask is todo, in_progress, blocked, review, done or dropped. A milestone is planned, active, done or dropped — it is never blocked or review, because those are facts about the items inside it."},
				      "progress_pct":{"type":"integer","minimum":0,"maximum":100,"description":"Work item only."},
				      "milestone_id":{"type":"string","description":"Work item only. The milestone's NAME, exactly as get_plan prints it — this field takes a name, not an id, because no id is ever shown to you. A milestone this same proposal creates earlier is a valid name here. Send \"\" to take the item out of its milestone."},
				      "assignee_id":{"type":"string","description":"The person's display name or email address, as get_plan prints them. Not an id. Send \"\" to unassign."},
				      "start_date":{"type":"string","description":"YYYY-MM-DD, a calendar day. \"\" clears it."},
				      "due_date":{"type":"string","description":"YYYY-MM-DD, a calendar day. \"\" clears it."},
				      "seq":{"type":"integer","description":"Ordering among siblings."},
				      "entity_refs":{"type":"array","items":{"type":"string"},
				                     "description":"Work item only. A JSON ARRAY of architecture entity slugs this item implements, such as [\"token-store\",\"auth-service\"]. The slugs must already exist in the graph. [] clears them. A bare string is not this field."},
				      "dependencies":{"type":"object","description":"Work item only. {\"blocks\": [refs], \"relates\": [refs]}, naming work items that already exist. blocks is directed: it means this item must finish before those can proceed. There is no blocked_by — say it from the other item. Sending only one key leaves the other kind alone; [] clears a kind.",
				        "properties":{
				          "blocks":{"type":"array","items":{"type":"string"}},
				          "relates":{"type":"array","items":{"type":"string"}}}}}}},
				  "required":["op","target"]}}},
				"required":["title","rationale","changes"]}`),
			Run: runProposePlanChange,
		},
		{
			Name: "propose_diagram_change",
			Kind: KindPropose,
			Description: "Raise a proposal for changes to an architecture diagram that already exists: its boxes and " +
				"its arrows. Node kinds and edge relations are a closed vocabulary — kinds: system, service, component, " +
				"datastore, queue, external_system, actor, api, job, boundary, note; relations: calls, depends_on, " +
				"reads_from, writes_to, publishes_to, subscribes_to, contains, deploys_to, data_flow. Use data_flow when " +
				"nothing more specific is true rather than inventing one. Changes apply in the order you send them, so a " +
				"node created by change 1 can be the end of an arrow drawn by change 2. A diagram itself is created and " +
				"deleted by a person at the canvas, never here.",
			Params: schema(`{"type":"object","properties":{
				"title":{"type":"string"},
				"rationale":{"type":"string"},
				"diagram":{"type":"string","description":"The slug of an existing diagram, as get_diagram prints it. This tool does not create one."},
				"changes":{"type":"array","minItems":1,"items":{"type":"object","properties":{
				  "op":{"type":"string","enum":["create","update","delete"],
				        "description":"On target diagram only update is possible — that renames it or changes its level."},
				  "target":{"type":"string","enum":["diagram_node","diagram_edge","diagram"]},
				  "after":{"type":"object","description":"A node takes slug, kind, name, summary, parent — and nothing else: what a box is made of goes in summary, in prose. An edge takes from, to, relation, label, where from and to are entity slugs of nodes that exist or that an earlier change in this proposal creates. The diagram itself takes name and level. A key not in these lists is refused, not ignored.",
				    "properties":{
				      "slug":{"type":"string","description":"Node only: the entity slug, which is its identity. Required to address one."},
				      "kind":{"type":"string","enum":["system","service","component","datastore","queue","external_system","actor","api","job","boundary","note"],
				              "description":"Node only, and required when creating one — a node cannot be without a kind."},
				      "name":{"type":"string","description":"A node's display name, or the diagram's own when the target is the diagram."},
				      "summary":{"type":"string","description":"Node only: what this thing is and what it is built from, in prose. This is the only place technology, ownership or anything else about the box belongs."},
				      "parent":{"type":"string","description":"Node only: the slug of the boundary or system that contains this node. Only a boundary or a system may contain anything."},
				      "from":{"type":"string","description":"Edge only: the entity slug the arrow leaves."},
				      "to":{"type":"string","description":"Edge only: the entity slug the arrow enters."},
				      "relation":{"type":"string","enum":["calls","depends_on","reads_from","writes_to","publishes_to","subscribes_to","contains","deploys_to","data_flow"],
				                  "description":"Edge only, and required: an arrow always says what kind it is."},
				      "label":{"type":"string","description":"Edge only: what travels along it."},
				      "level":{"type":"string","description":"Diagram only: its level in the architecture."}}}},
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

	// One checker across the whole proposal: the changes are an ordered list, so
	// change 2 is allowed to name the milestone change 1 creates, and the
	// project's milestones, members and entities are read once rather than once
	// per change.
	checker := newPlanChecker(s)

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

		ref := c.str("ref")
		// A create that names something is two different intentions and the
		// applier reads the wrong one: it creates a second row and discards the
		// ref, while the recorded `before` makes the proposal *render* as an edit
		// to the thing named. A reviewer approves what looks like a title change
		// and gets a duplicate work item. Refused rather than guessed, because
		// both readings are plausible and only the author knows which.
		if op == pr.OpCreate && target != pr.ChangeSubTask && ref != "" {
			return Result{}, refuse("change %d has op `create` and also names `%s`. Those say different things: "+
				"to change %s, send op `update`; to make something new, drop the `ref`. "+
				"A create that names an existing %s would quietly make a second one.",
				i+1, ref, ref, target)
		}
		change := pr.Change{Op: op, Target: target, After: c.obj("after")}
		if err := checker.check(ctx, changeCtx{
			i: i, target: target, op: op, ref: ref, after: change.After,
		}); err != nil {
			return Result{}, err
		}
		if ref != "" {
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
			// **Not** "propose creating it", which is what this used to say and
			// which cannot be done: a proposal edits a diagram that exists, and
			// the applier refuses any op but `update` on the picture itself. An
			// instruction the harness will refuse on the next call is worse than
			// no instruction — the model follows it, gets refused, and has
			// nowhere left to go.
			list, listErr := s.deps.Diagrams.List(ctx, s.turn.ProjectID, s.principal)
			var slugs []string
			if listErr == nil {
				for _, item := range list {
					slugs = append(slugs, strconv.Quote(item.Slug))
				}
			}
			return Result{}, refuse("there is no diagram %q in this project. The diagrams are: %s. "+
				"A diagram is created by a person at the canvas, not through a proposal — if the architecture "+
				"needs a picture that does not exist, say so with `add_note` or hand back.",
				slug, orNone(strings.Join(slugs, ", ")))
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

	// One checker across the proposal, holding the graph as it stands plus what
	// earlier changes add to it: an arrow may name a box the same proposal draws
	// two changes earlier, because the edits compose in order.
	checker := newDiagramChecker(view.Graph)

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
		if target == pr.ChangeDiagram && op != pr.OpUpdate {
			return Result{}, refuse("change %d would %s the diagram itself, and a proposal cannot: a picture is "+
				"created and removed by a person at the canvas. A proposal changes what is drawn in one — "+
				"its nodes and arrows — or renames it and changes its level, which is op `update`.", i+1, op)
		}

		after := c.obj("after")
		if err := checker.check(i, target, op, after); err != nil {
			return Result{}, err
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
