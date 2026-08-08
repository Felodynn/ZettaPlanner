package contextbuild

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
)

// selector decides how much of each layer survives the budget, and records
// every reduction as a Drop.
//
// It works by rendering under a set of limits, measuring, and tightening —
// rather than by estimating in advance and cutting once. Estimating in advance
// requires a second model of what rendering costs, and the two disagree exactly
// when the context is unusual, which is when the budget matters.
type selector struct {
	in    Input
	drops []Drop
}

func newSelector(in Input) *selector { return &selector{in: in} }

// limits is one reduction step.
//
// The order the steps are applied in is the whole policy, and it is a claim
// about value: a retrieved paragraph is worth less than a subtask list, which
// is worth less than knowing an item exists, which is worth less than knowing
// what has already been refused.
type limits struct {
	spill      int // -1 keeps every hit
	itemDetail bool
	hops       int
	openItems  int
	notes      int
	decisions  int
}

func unlimited() limits {
	return limits{spill: -1, itemDetail: true, hops: -1, openItems: -1, notes: -1, decisions: -1}
}

// ladder is the reduction sequence, loosest first.
//
// The order is the whole policy, and it is a claim about value: a retrieved
// paragraph is worth less than a subtask list, which is worth less than knowing
// an item exists, which is worth less than knowing what has already been
// refused. Rejections and deviations appear nowhere on it at any depth.
func ladderOf() []limits {
	ladder := []limits{unlimited()}

	// 1. Spill first: it is a similarity guess over prose, and the layer whose
	//    absence a reader is least likely to be misled by — provided the
	//    omission is stated, which it is.
	for _, n := range []int{8, 4, 2, 0} {
		l := ladder[len(ladder)-1]
		l.spill = n
		ladder = append(ladder, l)
	}
	// 2. Then the detail hanging off expanded items. "ZP-14 exists, is blocked,
	//    and blocks ZP-20" survives; its checklist does not.
	l := ladder[len(ladder)-1]
	l.itemDetail = false
	ladder = append(ladder, l)

	// 3. Then architecture hops, narrowing rather than vanishing: the entity
	//    stays named even when its neighbourhood is cut.
	for _, n := range []int{6, 3, 0} {
		l := ladder[len(ladder)-1]
		l.hops = n
		ladder = append(ladder, l)
	}
	// 4. Then the tail of the open-item list. Sorted blocked-first, so what
	//    goes is the least urgent end.
	for _, n := range []int{40, 20, 10} {
		l := ladder[len(ladder)-1]
		l.openItems = n
		ladder = append(ladder, l)
	}
	// 5. Then notes, then approved decisions. Rejections are not on this
	//    ladder at any depth, and neither are deviations.
	for _, n := range []int{10, 4} {
		l := ladder[len(ladder)-1]
		l.notes = n
		ladder = append(ladder, l)
	}
	for _, n := range []int{6, 3} {
		l := ladder[len(ladder)-1]
		l.decisions = n
		ladder = append(ladder, l)
	}
	return ladder
}

// sections renders under the loosest limits that fit, then reports what the
// tightening cost.
func (s *selector) sections() []Section {
	ladder := ladderOf()

	var (
		chosen   = ladder[0]
		rendered []Section
		fullCost int
	)
	for i, l := range ladder {
		rendered = s.render(l)
		cost := totalTokens(rendered) + EstimateTokens(headerFor(s.in))
		if i == 0 {
			fullCost = cost
		}
		chosen = l
		if cost <= s.in.Budget.Tokens {
			break
		}
	}

	s.recordDrops(chosen, fullCost-totalTokens(rendered))
	if s.in.Spill.Unavailable != "" {
		s.drops = append(s.drops, Drop{
			Layer: LayerSpill,
			What:  "the whole retrieval layer",
			Why:   s.in.Spill.Unavailable,
		})
	}
	return rendered
}

// recordDrops turns the chosen limits into human sentences naming what the
// budget cost, in the same words a reader would use.
func (s *selector) recordDrops(after limits, saved int) {
	why := "the assembly would otherwise exceed its " + itoa(s.in.Budget.Tokens) + "-token budget"

	if after.spill >= 0 && len(s.in.Spill.Hits) > after.spill {
		s.drops = append(s.drops, Drop{
			Layer:  LayerSpill,
			What:   fmt.Sprintf("%d of %d retrieved passages, weakest matches first", len(s.in.Spill.Hits)-after.spill, len(s.in.Spill.Hits)),
			Why:    why,
			Tokens: saved,
		})
	}
	if !after.itemDetail && expandedDetailCount(s.in.Expansion) > 0 {
		s.drops = append(s.drops, Drop{
			Layer: LayerExpansion,
			What:  "subtasks and checklists on the expanded work items",
			Why:   why,
		})
	}
	if after.hops >= 0 && hopCount(s.in.Expansion) > after.hops {
		s.drops = append(s.drops, Drop{
			Layer: LayerExpansion,
			What:  fmt.Sprintf("architecture relations beyond %d per entity", after.hops),
			Why:   why,
		})
	}
	if after.openItems >= 0 && len(s.in.Skeleton.OpenItems) > after.openItems {
		s.drops = append(s.drops, Drop{
			Layer: LayerSkeleton,
			What:  fmt.Sprintf("%d of %d open work items, least urgent first", len(s.in.Skeleton.OpenItems)-after.openItems, len(s.in.Skeleton.OpenItems)),
			Why:   why,
		})
	}
	if after.notes >= 0 && len(s.in.Skeleton.Notes) > after.notes {
		s.drops = append(s.drops, Drop{
			Layer: LayerSkeleton,
			What:  fmt.Sprintf("%d of %d open notes", len(s.in.Skeleton.Notes)-after.notes, len(s.in.Skeleton.Notes)),
			Why:   why,
		})
	}
	if after.decisions >= 0 && len(s.in.Skeleton.Decisions) > after.decisions {
		s.drops = append(s.drops, Drop{
			Layer: LayerSkeleton,
			What:  fmt.Sprintf("%d of %d recent accepted decisions", len(s.in.Skeleton.Decisions)-after.decisions, len(s.in.Skeleton.Decisions)),
			Why:   why,
		})
	}
}

// ── the sections ─────────────────────────────────────────────────────────────

func (s *selector) render(l limits) []Section {
	sk := s.in.Skeleton
	out := []Section{
		{Layer: LayerSkeleton, Title: "PROJECT", Body: renderProject(sk, s.in.Message)},
		{Layer: LayerSkeleton, Title: "OPEN WORK", Body: renderOpenWork(sk.OpenItems, l.openItems)},
		{Layer: LayerSkeleton, Title: "OPEN DEVIATIONS", Body: renderDeviations(sk.Deviations)},
		{Layer: LayerSkeleton, Title: "OPEN NOTES", Body: renderNotes(sk.Notes, l.notes)},
		{Layer: LayerSkeleton, Title: "RECENT DECISIONS", Body: renderDecisions(sk.Decisions, l.decisions)},
		{Layer: LayerSkeleton, Title: "PREVIOUSLY REJECTED", Body: renderRejections(sk.Rejections)},
		{Layer: LayerExpansion, Title: "REFERENCED WORK", Body: renderExpandedItems(s.in.Expansion.Items, l.itemDetail)},
		{Layer: LayerExpansion, Title: "ARCHITECTURE", Body: renderEntities(s.in.Expansion.Entities, l.hops)},
		{Layer: LayerSpill, Title: "FROM THE LOG", Body: renderSpill(s.in.Spill, l.spill)},
	}
	return out
}

func renderProject(sk Skeleton, message string) string {
	p := sk.Project
	var b strings.Builder
	b.WriteString("## PROJECT\n")
	b.WriteString("key: " + p.Key + "\n")
	b.WriteString("name: " + p.Name + "\n")
	if p.Description != "" {
		b.WriteString("description: " + oneLine(p.Description) + "\n")
	}
	b.WriteString("status: " + p.Status + "\n")
	if m := strings.TrimSpace(message); m != "" {
		b.WriteString("assembled for: " + oneLine(m) + "\n")
	}

	if len(sk.Milestones) > 0 {
		b.WriteString("\nmilestones:\n")
		for _, m := range sk.Milestones {
			b.WriteString("  - " + m.Milestone.Name + " [" + string(m.Milestone.Status) + "]")
			if d := plan.FormatDate(m.Milestone.DueDate); d != "" {
				b.WriteString(" due " + d)
			}
			b.WriteString(fmt.Sprintf("  (%d items, %d still open)\n", m.Total, m.Open))
			if m.Milestone.Intent != "" {
				b.WriteString("    intent: " + oneLine(m.Milestone.Intent) + "\n")
			}
		}
	}
	return b.String()
}

func renderOpenWork(items []ItemView, limit int) string {
	if len(items) == 0 {
		return "## OPEN WORK\n(nothing open)\n"
	}
	ordered := append([]ItemView(nil), items...)
	sortItems(ordered)
	if limit >= 0 && len(ordered) > limit {
		ordered = ordered[:limit]
	}

	var b strings.Builder
	b.WriteString("## OPEN WORK\n")
	b.WriteString("# ref · status · progress · assignee · title · dates. Blocked items first.\n")
	for _, v := range ordered {
		w := v.Item
		b.WriteString(fmt.Sprintf("%-8s %-12s %3d%%  %-16s %s",
			w.Ref, w.Status, w.ProgressPct, orDash(v.AssigneeName), w.Title))
		if w.Priority != "" {
			b.WriteString("  [" + string(w.Priority) + "]")
		}
		if d := plan.FormatDate(w.DueDate); d != "" {
			b.WriteString("  due " + d)
		}
		if v.MilestoneName != "" {
			b.WriteString("  {" + v.MilestoneName + "}")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderDeviations is never truncated.
//
// A deviation is the record that reality diverged from the plan, and it is
// usually the reason an item is stuck. Dropping one to save tokens produces an
// answer that reasons confidently from a plan that is known to be wrong.
func renderDeviations(devs []*plan.Deviation) string {
	if len(devs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## OPEN DEVIATIONS\n")
	b.WriteString("# Reality diverged from the plan here and nobody has resolved it yet.\n")
	for _, d := range devs {
		b.WriteString("[" + string(d.Severity) + "] ")
		if d.ItemRef != "" {
			b.WriteString(d.ItemRef + " — ")
		}
		b.WriteString(oneLine(d.Summary))
		b.WriteString("  (raised " + d.CreatedAt.UTC().Format("2006-01-02") + ")\n")
	}
	return b.String()
}

// renderNotes prints the open clarifications: overlaps, conflicts, missing
// pieces (§3.4).
//
// Notes are structured objects and are **never chunked into the vector store**.
// They arrive here by query, keyed to their subject, which is the property that
// makes them findable at all — a note broken into passages is a note that turns
// up in a similarity search about something else.
func renderNotes(notes []*diagram.Note, limit int) string {
	if len(notes) == 0 {
		return ""
	}
	shown := notes
	if limit >= 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	var b strings.Builder
	b.WriteString("## OPEN NOTES\n")
	b.WriteString("# Clarifications nobody has resolved. An `overlap` means two things may be the\n")
	b.WriteString("# same thing and the harness refused to decide — it is a question, not a fact.\n")
	for _, n := range shown {
		b.WriteString("[" + string(n.Kind) + "] ")
		if n.SubjectSlug != "" {
			b.WriteString(n.SubjectSlug + " — ")
		}
		b.WriteString(oneLine(n.Body) + "\n")
	}
	return b.String()
}

func renderDecisions(list []Decision, limit int) string {
	if len(list) == 0 {
		return ""
	}
	ordered := sortDecisions(list)
	if limit >= 0 && len(ordered) > limit {
		ordered = ordered[:limit]
	}
	var b strings.Builder
	b.WriteString("## RECENT DECISIONS\n")
	b.WriteString("# Proposals that were accepted. These are settled; build on them.\n")
	for _, d := range ordered {
		b.WriteString(d.DecidedAt.UTC().Format("2006-01-02") + "  " + d.Title + "\n")
		if d.Rationale != "" {
			b.WriteString("    proposed because: " + oneLine(d.Rationale) + "\n")
		}
		if d.DecisionRationale != "" {
			b.WriteString("    accepted because: " + oneLine(d.DecisionRationale) + "\n")
		}
	}
	return b.String()
}

// renderRejections is never truncated, at any budget.
//
// CONTRACT §5.2: rejected proposals with their rationales are first-class
// context. This is the field whose loss makes the agent re-propose what was
// already refused — and the human conclude the tool does not listen. It is
// cheaper than almost anything else in the assembly and worth more than most of
// it.
func renderRejections(list []Decision) string {
	if len(list) == 0 {
		return ""
	}
	ordered := sortDecisions(list)
	var b strings.Builder
	b.WriteString("## PREVIOUSLY REJECTED — do not propose these again\n")
	b.WriteString("# Each of these was put forward and refused, with the reason. Proposing one\n")
	b.WriteString("# again without addressing the reason is the single most annoying thing this\n")
	b.WriteString("# tool can do.\n")
	for _, d := range ordered {
		b.WriteString(d.DecidedAt.UTC().Format("2006-01-02") + "  " + d.Title + "\n")
		if d.Rationale != "" {
			b.WriteString("    proposed because: " + oneLine(d.Rationale) + "\n")
		}
		b.WriteString("    REJECTED because: " + oneLine(d.DecisionRationale) + "\n")
	}
	return b.String()
}

func renderExpandedItems(items []ExpandedItem, detail bool) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## REFERENCED WORK\n")
	b.WriteString("# Reached by traversal from what the request names — not by similarity.\n")
	b.WriteString("# Dependencies here are complete, not a sample.\n")
	for _, it := range items {
		w := it.Item
		b.WriteString("\n" + w.Ref + "  " + w.Title + "\n")
		if it.Reason != "" {
			b.WriteString("  pulled in because: " + it.Reason + "\n")
		}
		b.WriteString("  status: " + string(w.Status) + "  progress: " + itoa(w.ProgressPct) + "%")
		if it.AssigneeName != "" {
			b.WriteString("  assignee: " + it.AssigneeName)
		}
		if it.MilestoneName != "" {
			b.WriteString("  milestone: " + it.MilestoneName)
		}
		b.WriteString("\n")
		if s, d := plan.FormatDate(w.StartDate), plan.FormatDate(w.DueDate); s != "" || d != "" {
			b.WriteString("  dates: " + orDash(s) + " → " + orDash(d) + "\n")
		}
		if w.Intent != "" {
			b.WriteString("  intent: " + oneLine(w.Intent) + "\n")
		}
		if w.Acceptance != "" {
			b.WriteString("  acceptance: " + oneLine(w.Acceptance) + "\n")
		}
		writeDeps(&b, "  blocked by", it.BlockedBy, false)
		writeDeps(&b, "  blocks", it.Blocks, true)
		writeDeps(&b, "  relates to", it.Relates, true)
		if len(it.Refs) > 0 {
			var slugs []string
			for _, r := range it.Refs {
				slugs = append(slugs, r.EntitySlug)
			}
			b.WriteString("  implements: " + strings.Join(slugs, ", ") + "\n")
		}
		if !detail {
			continue
		}
		for _, st := range it.SubTasks {
			b.WriteString("    - subtask [" + string(st.Status) + "] " + st.Title + "\n")
		}
		for _, c := range it.Checklist {
			mark := " "
			if c.Checked {
				mark = "x"
			}
			b.WriteString("    - [" + mark + "] " + c.Text + "\n")
		}
	}
	return b.String()
}

func writeDeps(b *strings.Builder, label string, deps []plan.Dependency, far bool) {
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
	sort.Strings(parts)
	b.WriteString(label + ": " + strings.Join(parts, "; ") + "\n")
}

func renderEntities(slices []EntitySlice, hops int) string {
	if len(slices) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## ARCHITECTURE\n")
	b.WriteString("# The slice around the entities this work touches: each one plus one hop.\n")
	b.WriteString("# Relations are typed edges from the diagrams, not inferred from names.\n")
	for _, sl := range slices {
		e := sl.Entity
		b.WriteString("\n" + e.Slug + " (" + string(e.Kind) + ") " + e.Name + "\n")
		if e.Summary != "" {
			b.WriteString("  " + oneLine(e.Summary) + "\n")
		}
		shown := sl.Hops
		if hops >= 0 && len(shown) > hops {
			shown = shown[:hops]
		}
		for _, h := range shown {
			arrow := "→"
			if h.Inbound {
				arrow = "←"
			}
			b.WriteString("  " + arrow + " " + string(h.Relation) + " " + h.OtherSlug)
			if h.Label != "" {
				b.WriteString(" (" + h.Label + ")")
			}
			b.WriteString("  [drawn in " + h.DiagramSlug + "]\n")
		}
	}
	return b.String()
}

func renderSpill(sp Spill, limit int) string {
	if sp.Unavailable != "" {
		return "## FROM THE LOG\n(no retrieval was performed: " + sp.Unavailable + ")\n"
	}
	if len(sp.Hits) == 0 {
		return ""
	}
	shown := sp.Hits
	if limit >= 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	if len(shown) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## FROM THE LOG\n")
	b.WriteString("# Similarity matches over worklogs and messages only. These are passages that\n")
	b.WriteString("# resemble the request, not answers to it — the score says how alike, not how true.\n")
	for _, h := range shown {
		b.WriteString(fmt.Sprintf("\n%.2f  %s\n", h.Score, h.Label))
		for _, line := range strings.Split(strings.TrimSpace(h.Content), "\n") {
			b.WriteString("      " + strings.TrimSpace(line) + "\n")
		}
	}
	return b.String()
}

// ── helpers ──────────────────────────────────────────────────────────────────

func sortDecisions(in []Decision) []Decision {
	out := append([]Decision(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].DecidedAt.Equal(out[j].DecidedAt) {
			return out[i].DecidedAt.After(out[j].DecidedAt)
		}
		return out[i].Title < out[j].Title
	})
	return out
}

func totalTokens(sections []Section) int {
	var n int
	for _, s := range sections {
		n += EstimateTokens(s.Body)
	}
	return n
}

func headerFor(in Input) string {
	c := &Context{Budget: in.Budget}
	return c.header(in)
}

func expandedDetailCount(e Expansion) int {
	var n int
	for _, it := range e.Items {
		n += len(it.SubTasks) + len(it.Checklist)
	}
	return n
}

func hopCount(e Expansion) int {
	var n int
	for _, s := range e.Entities {
		n += len(s.Hops)
	}
	return n
}

// oneLine flattens prose so a multi-paragraph intent cannot break the line
// structure the rest of the document relies on. Truncated with an explicit
// marker rather than silently.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 400
	if len(s) > max {
		return s[:max] + "… [truncated]"
	}
	return s
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
