package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
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
		if ref := c.str("ref"); ref != "" {
			id, before, err := s.resolvePlanTarget(ctx, target, ref)
			if err != nil {
				return Result{}, err
			}
			change.TargetID = &id
			change.Before = before
		}
		if op != pr.OpCreate && change.TargetID == nil {
			return Result{}, refuse("change %d is an %s with no `ref`; say which %s it changes.", i+1, op, target)
		}
		changes = append(changes, change)
	}
	return s.raiseProposal(ctx, title, rationale, changes)
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

		id := diagram.ID
		changes = append(changes, pr.Change{Op: op, Target: target, TargetID: &id, After: after})
	}
	return s.raiseProposal(ctx, title, rationale, changes)
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
