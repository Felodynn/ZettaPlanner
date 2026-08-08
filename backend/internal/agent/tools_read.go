package agent

import (
	"context"
	"fmt"
	"strings"

	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
)

// Read-only tools. None of these produce output in the sense that ends a turn
// successfully: an agent that read eight things and changed nothing has not
// answered anything, and the `no_output` verdict says so.

func readTools() []Tool {
	return []Tool{
		{
			Name:        "get_plan",
			Kind:        KindRead,
			Description: "The project's milestones and open work items, with status, assignee and dates.",
			Params: schema(`{"type":"object","properties":{
				"status":{"type":"string","enum":["todo","in_progress","blocked","review","done","dropped"],
				          "description":"Restrict to one status. Omit for every open item."},
				"assignee_only":{"type":"boolean",
				          "description":"Restrict to items assigned to the person who asked for this turn."}},
				"required":[]}`),
			Run: runGetPlan,
		},
		{
			Name:        "get_item",
			Kind:        KindRead,
			Description: "One work item by its ref (ZP-14): its fields, dependencies both ways, subtasks, checklist and the architecture it claims to implement.",
			Params: schema(`{"type":"object","properties":{
				"ref":{"type":"string","description":"The human-facing ref, such as ZP-14."}},
				"required":["ref"]}`),
			Run: runGetItem,
		},
		{
			Name:        "get_diagram",
			Kind:        KindRead,
			Description: "One diagram as canonical YAML, by its slug. Contains no coordinates: a diagram's meaning is its nodes and relations, never its geometry.",
			Params: schema(`{"type":"object","properties":{
				"slug":{"type":"string","description":"The diagram's stable slug, such as auth-context."}},
				"required":["slug"]}`),
			Run: runGetDiagram,
		},
		{
			Name:        "get_entity",
			Kind:        KindRead,
			Description: "One architecture entity by slug: what it is, which diagrams draw it, and what depends on it across all of them.",
			Params: schema(`{"type":"object","properties":{
				"slug":{"type":"string","description":"The entity slug, such as token-store."}},
				"required":["slug"]}`),
			Run: runGetEntity,
		},
		{
			Name:        "search_worklogs",
			Kind:        KindRead,
			Description: "Similarity search over worklogs and past messages only. Returns passages that resemble the query, not answers to it. Structured things — items, notes, deviations, the graph — have their own tools and are not searched here.",
			Params: schema(`{"type":"object","properties":{
				"query":{"type":"string","description":"What to look for, in words."},
				"limit":{"type":"integer","minimum":1,"maximum":20}},
				"required":["query"]}`),
			Run: runSearchWorklogs,
		},
		{
			Name:        "list_notes",
			Kind:        KindRead,
			Description: "Open clarifications: overlaps, conflicts, missing pieces. An `overlap` means two things may be the same thing and the harness refused to decide — it is a question, not a fact.",
			Params:      schema(`{"type":"object","properties":{},"required":[]}`),
			Run:         runListNotes,
		},
		{
			Name:        "list_deviations",
			Kind:        KindRead,
			Description: "Recorded divergences between the plan and what actually happened, unresolved by default.",
			Params: schema(`{"type":"object","properties":{
				"include_resolved":{"type":"boolean"}},"required":[]}`),
			Run: runListDeviations,
		},
		{
			Name:        "get_digest",
			Kind:        KindRead,
			Description: "The project digest. The `harness` half is computed from the database and is fact; the `model` half is a previous run's hypothesis and may be stale — treat it as a lead, never as a record.",
			Params:      schema(`{"type":"object","properties":{},"required":[]}`),
			Run:         runGetDigest,
		},
	}
}

func runGetPlan(ctx context.Context, s *session, a args) (Result, error) {
	f := repo.WorkItemFilter{Status: pl.Status(a.str("status"))}
	if only, ok := a.boolVal("assignee_only"); ok && only {
		id := s.principal.UserID
		f.AssigneeID = &id
	}

	milestones, err := s.deps.Plan.ListMilestones(ctx, s.turn.ProjectID, s.principal)
	if err != nil {
		return Result{}, err
	}
	items, err := s.deps.Plan.ListWorkItems(ctx, s.turn.ProjectID, f, s.principal)
	if err != nil {
		return Result{}, err
	}

	var b strings.Builder
	b.WriteString("milestones:\n")
	for _, m := range milestones {
		fmt.Fprintf(&b, "  %s [%s] due %s\n", m.Name, m.Status, orNone(pl.FormatDate(m.DueDate)))
	}
	if len(milestones) == 0 {
		b.WriteString("  (none)\n")
	}

	b.WriteString("\nwork items:\n")
	var shown int
	for _, w := range items {
		if f.Status == "" && !w.Status.Open() {
			continue
		}
		shown++
		fmt.Fprintf(&b, "  %-8s %-12s %3d%%  %s", w.Ref, w.Status, w.ProgressPct, w.Title)
		if d := pl.FormatDate(w.DueDate); d != "" {
			fmt.Fprintf(&b, "  due %s", d)
		}
		if w.AssigneeID != nil && *w.AssigneeID == s.principal.UserID {
			b.WriteString("  (assigned to the person who asked)")
		}
		b.WriteString("\n")
	}
	if shown == 0 {
		b.WriteString("  (none matching)\n")
	}
	return Result{Content: b.String()}, nil
}

func runGetItem(ctx context.Context, s *session, a args) (Result, error) {
	ref, err := a.required("ref")
	if err != nil {
		return Result{}, err
	}
	d, err := s.deps.Plan.ItemByRef(ctx, s.turn.ProjectID, ref, s.principal)
	if err != nil {
		if isNotFound(err) {
			return Result{}, refuse("there is no work item %s in this project. "+
				"Call `get_plan` to see what exists rather than guessing another ref.", strings.ToUpper(ref))
		}
		return Result{}, err
	}

	w := d.Item
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n", w.Ref, w.Title)
	fmt.Fprintf(&b, "status: %s  progress: %d%%  priority: %s\n", w.Status, w.ProgressPct, orNone(string(w.Priority)))
	fmt.Fprintf(&b, "assignee: %s  milestone: %s\n", orNone(d.AssigneeName), orNone(milestoneName(d)))
	fmt.Fprintf(&b, "dates: %s → %s\n", orNone(pl.FormatDate(w.StartDate)), orNone(pl.FormatDate(w.DueDate)))
	if w.Intent != "" {
		fmt.Fprintf(&b, "intent: %s\n", w.Intent)
	}
	if w.Acceptance != "" {
		fmt.Fprintf(&b, "acceptance: %s\n", w.Acceptance)
	}
	if w.AssigneeID != nil && *w.AssigneeID == s.principal.UserID {
		b.WriteString("This item is assigned to the person who asked for this turn, " +
			"so `update_dev_field` may write its status and progress_pct directly.\n")
	} else {
		b.WriteString("This item is NOT assigned to the person who asked for this turn. " +
			"Even status and progress_pct are out of reach here; changes go through `propose_plan_change`.\n")
	}

	writeDeps(&b, "blocked by", d.BlockedBy, false)
	writeDeps(&b, "blocks", d.Blocks, true)
	writeDeps(&b, "relates to", d.Relates, true)
	for _, st := range d.SubTasks {
		fmt.Fprintf(&b, "  subtask [%s] %s\n", st.Status, st.Title)
	}
	for _, c := range d.Checklist {
		mark := " "
		if c.Checked {
			mark = "x"
		}
		fmt.Fprintf(&b, "  [%s] %s\n", mark, c.Text)
	}
	if len(d.Refs) > 0 {
		var slugs []string
		for _, r := range d.Refs {
			slugs = append(slugs, r.EntitySlug)
		}
		fmt.Fprintf(&b, "implements: %s\n", strings.Join(slugs, ", "))
	}
	return Result{Content: b.String()}, nil
}

func runGetDiagram(ctx context.Context, s *session, a args) (Result, error) {
	slug, err := a.required("slug")
	if err != nil {
		return Result{}, err
	}
	d, err := s.deps.Diagrams.BySlug(ctx, s.turn.ProjectID, slug, s.principal)
	if err != nil {
		if isNotFound(err) {
			list, listErr := s.deps.Diagrams.List(ctx, s.turn.ProjectID, s.principal)
			if listErr == nil {
				var slugs []string
				for _, item := range list {
					slugs = append(slugs, item.Slug)
				}
				return Result{}, refuse("there is no diagram %q in this project. The diagrams are: %s",
					slug, orNone(strings.Join(slugs, ", ")))
			}
		}
		return Result{}, err
	}
	yaml, err := s.deps.Diagrams.YAML(ctx, d.ID, s.principal)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: string(yaml)}, nil
}

func runGetEntity(ctx context.Context, s *session, a args) (Result, error) {
	slug, err := a.required("slug")
	if err != nil {
		return Result{}, err
	}
	entities, err := s.deps.Diagrams.Entities(ctx, s.turn.ProjectID, s.principal)
	if err != nil {
		return Result{}, err
	}
	var found *dg.Entity
	for _, e := range entities {
		if e.Slug == slug {
			found = e
			break
		}
	}
	if found == nil {
		var slugs []string
		for _, e := range entities {
			slugs = append(slugs, e.Slug)
		}
		return Result{}, refuse("there is no entity %q in this project. The entities are: %s",
			slug, orNone(strings.Join(slugs, ", ")))
	}

	detail, err := s.deps.Diagrams.Entity(ctx, found.ID, 0, s.principal)
	if err != nil {
		return Result{}, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s) %s\n", found.Slug, found.Kind, found.Name)
	if found.Summary != "" {
		fmt.Fprintf(&b, "%s\n", found.Summary)
	}
	if len(detail.Placements) > 0 {
		b.WriteString("drawn in:\n")
		for _, p := range detail.Placements {
			fmt.Fprintf(&b, "  %s (%s)\n", p.DiagramSlug, p.Level)
		}
	}
	if len(detail.Dependents) > 0 {
		b.WriteString("depended on by:\n")
		for _, r := range detail.Dependents {
			fmt.Fprintf(&b, "  %s %s via %s\n", r.Slug, r.Name, r.Relation)
		}
	}
	if len(detail.DependsOn) > 0 {
		b.WriteString("depends on:\n")
		for _, r := range detail.DependsOn {
			fmt.Fprintf(&b, "  %s %s via %s\n", r.Slug, r.Name, r.Relation)
		}
	}
	return Result{Content: b.String()}, nil
}

func runSearchWorklogs(ctx context.Context, s *session, a args) (Result, error) {
	query, err := a.required("query")
	if err != nil {
		return Result{}, err
	}
	limit, _ := a.intVal("limit")
	hits, unavailable, err := s.deps.Context.SearchSpill(ctx, s.turn.ProjectID, query, limit, s.principal)
	if err != nil {
		return Result{}, err
	}
	if unavailable != "" {
		// An answer, not a failure. The model should stop asking rather than
		// try a different phrasing against a search that is not running.
		return Result{Content: "No search was performed: " + unavailable +
			"\nDo not retry this tool in this turn; the reason will not change."}, nil
	}
	if len(hits) == 0 {
		return Result{Content: "Nothing in the worklogs or messages resembles that. " +
			"This is a real answer about the project, not a failure of the search."}, nil
	}

	var b strings.Builder
	for _, h := range hits {
		fmt.Fprintf(&b, "%.2f  %s\n      %s\n\n", h.Score, h.Label, strings.TrimSpace(h.Content))
	}
	return Result{Content: b.String()}, nil
}

func runListNotes(ctx context.Context, s *session, _ args) (Result, error) {
	notes, err := s.deps.Diagrams.Notes(ctx, s.turn.ProjectID, dg.NoteOpen, s.principal)
	if err != nil {
		return Result{}, err
	}
	if len(notes) == 0 {
		return Result{Content: "No open notes."}, nil
	}
	var b strings.Builder
	for _, n := range notes {
		fmt.Fprintf(&b, "[%s] %s%s\n", n.Kind, subjectPrefix(n.SubjectSlug), n.Body)
	}
	return Result{Content: b.String()}, nil
}

func runListDeviations(ctx context.Context, s *session, a args) (Result, error) {
	includeResolved, _ := a.boolVal("include_resolved")
	list, err := s.deps.Plan.ListDeviations(ctx, s.turn.ProjectID,
		repo.DeviationFilter{OpenOnly: !includeResolved}, s.principal)
	if err != nil {
		return Result{}, err
	}
	if len(list) == 0 {
		return Result{Content: "No deviations recorded."}, nil
	}
	var b strings.Builder
	for _, d := range list {
		fmt.Fprintf(&b, "[%s] %s%s", d.Severity, subjectPrefix(d.ItemRef), d.Summary)
		if d.Resolved() {
			fmt.Fprintf(&b, "  (resolved: %s)", d.Resolution)
		}
		b.WriteString("\n")
	}
	return Result{Content: b.String()}, nil
}

func runGetDigest(ctx context.Context, s *session, _ args) (Result, error) {
	d, err := s.deps.Context.Digest(ctx, s.turn.ProjectID, s.principal)
	if err != nil {
		return Result{}, err
	}
	var b strings.Builder
	h := d.Harness
	b.WriteString("computed from the database — these are facts:\n")
	fmt.Fprintf(&b, "  work items: %d (%d open, %d unassigned)\n", h.WorkItems, h.OpenItems, h.UnassignedOpen)
	fmt.Fprintf(&b, "  by status: %v\n", h.ByStatus)
	fmt.Fprintf(&b, "  blocked: %v\n", h.BlockedRefs)
	fmt.Fprintf(&b, "  open deviations: %d %v\n", h.OpenDeviations, h.BySeverity)
	fmt.Fprintf(&b, "  open notes: %d\n", h.OpenNotes)
	fmt.Fprintf(&b, "  active milestone: %s\n", orNone(h.ActiveMilestone))

	if d.Model.Empty() {
		b.WriteString("\nno previous run has recorded a hypothesis.\n")
		return Result{Content: b.String()}, nil
	}
	staleness := "current"
	if d.ModelStale {
		staleness = "STALE — the project has changed since this was written"
	}
	fmt.Fprintf(&b, "\nwritten by a previous run (%s) — a hypothesis, not a record:\n", staleness)
	if d.Model.CurrentHypothesis != "" {
		fmt.Fprintf(&b, "  hypothesis: %s\n", d.Model.CurrentHypothesis)
	}
	for _, e := range d.Model.DeadEnds {
		fmt.Fprintf(&b, "  dead end: %s\n", e)
	}
	for _, q := range d.Model.OpenQuestions {
		fmt.Fprintf(&b, "  open question: %s\n", q)
	}
	return Result{Content: b.String()}, nil
}

// ── formatting ───────────────────────────────────────────────────────────────

func writeDeps(b *strings.Builder, label string, deps []pl.Dependency, far bool) {
	if len(deps) == 0 {
		return
	}
	var parts []string
	for _, d := range deps {
		if far {
			parts = append(parts, d.ToRef+" "+d.ToTitle)
		} else {
			parts = append(parts, d.FromRef+" "+d.FromTitle)
		}
	}
	fmt.Fprintf(b, "%s: %s\n", label, strings.Join(parts, "; "))
}

// milestoneName reads the milestone off an item detail, or "" when it has none.
func milestoneName(d *plansvc.ItemDetail) string {
	if d.Milestone == nil {
		return ""
	}
	return d.Milestone.Name
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

func subjectPrefix(s string) string {
	if s == "" {
		return ""
	}
	return s + " — "
}
