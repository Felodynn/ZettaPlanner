// Package spec renders spec.md: the handoff document for a work item or a
// milestone.
//
// **The reader is not in the room.** Contract §10 defers the Lumix integration
// to v0.1, so in v0 a person exports this by hand and pastes it somewhere else
// — into another planning system, into a model, into an email. Whoever reads it
// has no access to this database, cannot follow a link, and cannot ask a
// question. Everything they need to decompose the work has to be in the bytes.
//
// That is why the architecture arrives as the **canonical serialized slice**
// (§6.2) rather than as a picture or a paraphrase. A decomposer given prose
// alone invents component names; given the slice, it uses the ones that exist.
//
// Two rules hold here:
//
//  1. **Nothing is invented.** Every line traces to a stored field. Where a
//     field is empty the document says so in words rather than omitting the
//     heading, because a missing "Acceptance criteria" section reads as an
//     oversight in the export and an explicit "none recorded" reads as a fact
//     about the plan — which it is, and an important one.
//
//  2. **Rendering is pure.** This package has no database and no clock; the
//     export time is a field. Which means the whole document is testable, and
//     the awkward part — deciding what counts as a constraint — is testable
//     without a fixture.
package spec

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Kind is what was exported.
type Kind string

const (
	KindWorkItem  Kind = "work_item"
	KindMilestone Kind = "milestone"
)

// Project identifies where the work lives.
type Project struct {
	Key     string
	Name    string
	Summary string
}

// Link is one end of a dependency, named as a human says it.
type Link struct {
	Ref     string
	Title   string
	Status  string
	DueDate string
}

// SubTask is one recorded leaf of decomposition.
type SubTask struct {
	Title  string
	Status string
}

// Check is one checklist box.
type Check struct {
	Text    string
	Checked bool
}

// Deviation is a recorded divergence between the plan and reality.
type Deviation struct {
	Summary    string
	Severity   string
	Resolution string
	Resolved   bool
	At         string
}

// Note is an unresolved question about the architecture the item touches.
type Note struct {
	Kind    string
	Subject string
	Body    string
}

// Unverified names an entity the vision model read off a drawing that no human
// has since confirmed (§3.7). It is a constraint on the reader's confidence,
// not on the work, and saying so is the difference between a decomposer that
// asks and one that assumes.
type Unverified struct {
	Slug string
	Name string
}

// Item is one work item, with everything the document says about it.
type Item struct {
	Ref         string
	Title       string
	Intent      string
	Acceptance  string
	Status      string
	Priority    string
	ProgressPct int
	Assignee    string
	StartDate   string
	DueDate     string
	Milestone   string
	MilestoneDu string
	Provenance  string

	SubTasks  []SubTask
	Checklist []Check

	// BlockedBy is what must finish first; Blocks is what waits on this. Both
	// are constraints and they are different ones: the first bounds when the
	// work can start, the second bounds what its interface may not break.
	BlockedBy []Link
	Blocks    []Link
	Relates   []Link

	Deviations []Deviation

	// BlockerTrouble is the open deviations on the items blocking this one.
	//
	// Found by the Phase 8 manual run and worth the extra reads: a reader told
	// "blocked by ZP-1, in progress, due the 18th" plans as though it lands on
	// the 18th. The deviation on ZP-1 says it will not. The milestone export
	// surfaced it because it aggregates every item's deviations, and the item
	// export did not — one document contradicting the other about the same
	// week. Recorded as D64.
	BlockerTrouble []BlockerIssue

	EntitySlugs []string
}

// BlockerIssue is a deviation open against something this item waits on.
type BlockerIssue struct {
	Ref      string
	Title    string
	Summary  string
	Severity string
}

// Milestone is one milestone and the items under it.
type Milestone struct {
	Name      string
	Intent    string
	Status    string
	StartDate string
	DueDate   string
	Items     []Item
}

// Export is a whole document, gathered and ready to render.
type Export struct {
	Kind       Kind
	Project    Project
	ExportedAt time.Time
	ExportedBy string

	Item      *Item
	Milestone *Milestone

	// Architecture is the canonical YAML slice (§6.2), already serialized. Nil
	// when the subject references no entities.
	Architecture []byte

	// Notes are unresolved questions about the entities in the slice, and
	// Unverified is what in it was never checked by a person.
	Notes      []Note
	Unverified []Unverified
}

// Render produces the document.
func Render(e Export) []byte {
	var b strings.Builder
	switch e.Kind {
	case KindMilestone:
		renderMilestone(&b, e)
	default:
		renderItem(&b, e)
	}
	return []byte(b.String())
}

// Filename is what the export should be saved as. Derived from the ref or the
// milestone name so a directory of them is readable.
func Filename(e Export) string {
	switch {
	case e.Kind == KindMilestone && e.Milestone != nil:
		return slugish(e.Milestone.Name) + ".spec.md"
	case e.Item != nil:
		return strings.ToLower(e.Item.Ref) + ".spec.md"
	default:
		return "spec.md"
	}
}

// ── work item ────────────────────────────────────────────────────────────────

func renderItem(b *strings.Builder, e Export) {
	it := e.Item
	if it == nil {
		b.WriteString("# spec\n\nNothing was exported.\n")
		return
	}

	fmt.Fprintf(b, "# %s — %s\n\n", it.Ref, it.Title)
	preamble(b, e)

	facts := [][2]string{
		{"Status", statusLine(it.Status, it.ProgressPct)},
		{"Priority", orNone(it.Priority, "not triaged")},
		{"Milestone", milestoneLine(it.Milestone, it.MilestoneDu)},
		{"Assignee", orNone(it.Assignee, "unassigned")},
		{"Dates", dateLine(it.StartDate, it.DueDate)},
		{"Origin", orNone(it.Provenance, "unrecorded")},
	}
	for _, f := range facts {
		fmt.Fprintf(b, "- **%s:** %s\n", f[0], f[1])
	}
	b.WriteString("\n")

	section(b, "Intent", it.Intent,
		"No intent was recorded for this item. What it is *for* is therefore not "+
			"stated anywhere, and a decomposition built from the title alone is a guess.")

	b.WriteString("## Acceptance criteria\n\n")
	if strings.TrimSpace(it.Acceptance) == "" && len(it.Checklist) == 0 {
		b.WriteString("*None recorded.* Nobody has written down what would make this done, " +
			"which means \"done\" is currently whoever's judgement gets there first.\n\n")
	} else {
		if s := strings.TrimSpace(it.Acceptance); s != "" {
			b.WriteString(s + "\n\n")
		}
		for _, c := range it.Checklist {
			box := " "
			if c.Checked {
				box = "x"
			}
			fmt.Fprintf(b, "- [%s] %s\n", box, c.Text)
		}
		if len(it.Checklist) > 0 {
			b.WriteString("\n")
		}
	}

	renderConstraints(b, constraintsFor(*it, e.Notes, e.Unverified))

	b.WriteString("## Decomposition already recorded\n\n")
	if len(it.SubTasks) == 0 {
		b.WriteString("*None.* This item has not been broken down, so the decomposition is open.\n\n")
	} else {
		for i, st := range it.SubTasks {
			fmt.Fprintf(b, "%d. `%s` %s\n", i+1, st.Status, st.Title)
		}
		b.WriteString("\nThese exist already. A decomposition that re-derives them produces duplicates " +
			"of work somebody is possibly already doing.\n\n")
	}

	renderArchitecture(b, e, it.EntitySlugs)
}

// ── milestone ────────────────────────────────────────────────────────────────

func renderMilestone(b *strings.Builder, e Export) {
	m := e.Milestone
	if m == nil {
		b.WriteString("# spec\n\nNothing was exported.\n")
		return
	}

	fmt.Fprintf(b, "# %s\n\n", m.Name)
	preamble(b, e)

	open := 0
	for _, it := range m.Items {
		if !finished(it.Status) {
			open++
		}
	}
	fmt.Fprintf(b, "- **Status:** %s\n", orNone(m.Status, "unset"))
	fmt.Fprintf(b, "- **Dates:** %s\n", dateLine(m.StartDate, m.DueDate))
	fmt.Fprintf(b, "- **Work items:** %d, of which %d are still open\n\n", len(m.Items), open)

	section(b, "Intent", m.Intent,
		"No intent was recorded for this milestone. What it is *for* is therefore not stated "+
			"anywhere, and its items are the only evidence of what it means.")

	b.WriteString("## Work items\n\n")
	if len(m.Items) == 0 {
		b.WriteString("*None.* The milestone is empty.\n\n")
	}
	for _, it := range m.Items {
		fmt.Fprintf(b, "### %s — %s\n\n", it.Ref, it.Title)
		fmt.Fprintf(b, "`%s`", it.Status)
		if it.Priority != "" {
			fmt.Fprintf(b, " · priority %s", it.Priority)
		}
		if it.Assignee != "" {
			fmt.Fprintf(b, " · %s", it.Assignee)
		}
		if it.DueDate != "" {
			fmt.Fprintf(b, " · due %s", it.DueDate)
		}
		b.WriteString("\n\n")

		if s := strings.TrimSpace(it.Intent); s != "" {
			b.WriteString("**Intent.** " + oneParagraph(s) + "\n\n")
		} else {
			b.WriteString("**Intent.** *Not recorded.*\n\n")
		}
		if s := strings.TrimSpace(it.Acceptance); s != "" {
			b.WriteString("**Done when.** " + oneParagraph(s) + "\n\n")
		} else {
			b.WriteString("**Done when.** *Not recorded.*\n\n")
		}
		for _, st := range it.SubTasks {
			fmt.Fprintf(b, "- `%s` %s\n", st.Status, st.Title)
		}
		if len(it.SubTasks) > 0 {
			b.WriteString("\n")
		}
	}

	renderConstraints(b, milestoneConstraints(*m, e.Notes, e.Unverified))
	renderArchitecture(b, e, milestoneSlugs(*m))
}

// ── shared pieces ────────────────────────────────────────────────────────────

func preamble(b *strings.Builder, e Export) {
	fmt.Fprintf(b, "> Exported from ZettaPlanner project **%s**", e.Project.Key)
	if e.Project.Name != "" {
		fmt.Fprintf(b, " (%s)", e.Project.Name)
	}
	if !e.ExportedAt.IsZero() {
		fmt.Fprintf(b, " on %s", e.ExportedAt.UTC().Format(time.RFC3339))
	}
	if e.ExportedBy != "" {
		fmt.Fprintf(b, " by %s", e.ExportedBy)
	}
	b.WriteString(".\n>\n")
	b.WriteString("> This is a **snapshot**, and it is one-way: nothing reads it back in. " +
		"The plan it came from may have moved since, and edits made to this file reach nobody.\n\n")
	if s := strings.TrimSpace(e.Project.Summary); s != "" {
		b.WriteString(oneParagraph(s) + "\n\n")
	}
}

func section(b *strings.Builder, heading, body, whenEmpty string) {
	fmt.Fprintf(b, "## %s\n\n", heading)
	if s := strings.TrimSpace(body); s != "" {
		b.WriteString(s + "\n\n")
		return
	}
	b.WriteString("*" + whenEmpty + "*\n\n")
}

func renderArchitecture(b *strings.Builder, e Export, slugs []string) {
	b.WriteString("## Architecture\n\n")
	if len(e.Architecture) == 0 {
		if len(slugs) == 0 {
			b.WriteString("*No architecture is referenced.* Nobody has linked this to a catalog entity, " +
				"so the components it touches are not recorded — which is a gap in the plan, not in " +
				"the export.\n\n")
			return
		}
		b.WriteString("*The referenced entities could not be resolved.* They were: `" +
			strings.Join(slugs, "`, `") + "`.\n\n")
		return
	}

	b.WriteString("The canonical serialization of the entities this references, plus the relations " +
		"drawn around them. `referenced: false` marks an entity that appears only as the far end " +
		"of a relation — context, not scope. **Use these names.** They are the ones the rest of " +
		"the project uses, and a decomposition that invents its own cannot be matched back.\n\n")
	b.WriteString("```yaml\n")
	b.Write(e.Architecture)
	if !strings.HasSuffix(string(e.Architecture), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")
}

// ── constraints ──────────────────────────────────────────────────────────────

// constraintsFor derives what bounds the work.
//
// **`constraints` is not a stored field**, and inventing one would have been the
// wrong answer: a free-text box labelled constraints fills up with things that
// are already recorded elsewhere and then disagrees with them. What actually
// constrains a work item is recorded — its blockers, its dates against its
// milestone's, the deviations open against it, and how much of the architecture
// it touches has never been checked by a person — so the export reads those and
// says them in sentences. Recorded as D61.
func constraintsFor(it Item, notes []Note, unverified []Unverified) []string {
	var out []string

	for _, l := range it.BlockedBy {
		if finished(l.Status) {
			continue
		}
		out = append(out, fmt.Sprintf("**Blocked by %s** — %s. It is `%s`%s, so this cannot finish before it does.",
			l.Ref, l.Title, l.Status, dueClause(l.DueDate)))
	}
	for _, b := range it.BlockerTrouble {
		out = append(out, fmt.Sprintf("**%s, which blocks this, has already diverged (%s):** %s. "+
			"Its date is not a date to plan against until somebody decides what to do about that.",
			b.Ref, orNone(b.Severity, "unrated"), strings.TrimSuffix(strings.TrimSpace(b.Summary), ".")))
	}
	for _, l := range it.Blocks {
		if finished(l.Status) {
			continue
		}
		out = append(out, fmt.Sprintf("**%s is waiting on this** — %s. Whatever this produces is an interface "+
			"something else has already been planned against.", l.Ref, l.Title))
	}
	for _, l := range it.Relates {
		out = append(out, fmt.Sprintf("Related to %s — %s. Not a hard dependency; recorded because somebody "+
			"thought the two would be worked on together.", l.Ref, l.Title))
	}

	if it.DueDate != "" {
		line := fmt.Sprintf("**Due %s.**", it.DueDate)
		if it.StartDate != "" {
			line = fmt.Sprintf("**Runs %s to %s.**", it.StartDate, it.DueDate)
		}
		if it.MilestoneDu != "" {
			line += fmt.Sprintf(" Its milestone %s is due %s.", it.Milestone, it.MilestoneDu)
		}
		out = append(out, line)
	}

	for _, d := range it.Deviations {
		if d.Resolved {
			continue
		}
		line := fmt.Sprintf("**Open deviation (%s):** %s", orNone(d.Severity, "unrated"), d.Summary)
		if !strings.HasSuffix(strings.TrimSpace(line), ".") {
			line += "."
		}
		out = append(out, line+" Reality has already diverged from the plan here, and nothing has been "+
			"decided about it.")
	}

	return append(out, architectureConstraints(notes, unverified)...)
}

func milestoneConstraints(m Milestone, notes []Note, unverified []Unverified) []string {
	var out []string

	if m.DueDate != "" {
		out = append(out, fmt.Sprintf("**The milestone is due %s.**%s", m.DueDate, startClause(m.StartDate)))
	}

	inside := make(map[string]bool, len(m.Items))
	for _, it := range m.Items {
		inside[it.Ref] = true
	}
	// Only dependencies that leave the milestone. A blocker inside it is
	// visible in the item list above, and repeating it here buries the ones
	// that are somebody else's problem.
	seen := map[string]bool{}
	for _, it := range m.Items {
		for _, l := range it.BlockedBy {
			if inside[l.Ref] || finished(l.Status) || seen[it.Ref+l.Ref] {
				continue
			}
			seen[it.Ref+l.Ref] = true
			out = append(out, fmt.Sprintf("**%s depends on %s, which is outside this milestone** — %s (`%s`%s).",
				it.Ref, l.Ref, l.Title, l.Status, dueClause(l.DueDate)))
		}
	}
	for _, it := range m.Items {
		for _, d := range it.Deviations {
			if d.Resolved {
				continue
			}
			out = append(out, fmt.Sprintf("**Open deviation on %s (%s):** %s", it.Ref,
				orNone(d.Severity, "unrated"), d.Summary))
		}
	}

	return append(out, architectureConstraints(notes, unverified)...)
}

// architectureConstraints turns unresolved questions about the architecture into
// sentences a reader can act on.
func architectureConstraints(notes []Note, unverified []Unverified) []string {
	var out []string
	for _, n := range notes {
		subject := n.Subject
		if subject == "" {
			subject = "the architecture"
		} else {
			subject = "`" + subject + "`"
		}
		out = append(out, fmt.Sprintf("**Unresolved %s on %s:** %s", n.Kind, subject, oneParagraph(n.Body)))
	}
	if len(unverified) > 0 {
		names := make([]string, 0, len(unverified))
		for _, u := range unverified {
			names = append(names, "`"+u.Slug+"`")
		}
		sort.Strings(names)
		out = append(out, fmt.Sprintf("**Read off a drawing and never confirmed:** %s. "+
			"A vision model produced these and no human has corrected or approved them, so treat "+
			"their kind and their relations as a reading rather than a decision.",
			strings.Join(names, ", ")))
	}
	return out
}

func renderConstraints(b *strings.Builder, list []string) {
	b.WriteString("## Constraints\n\n")
	if len(list) == 0 {
		b.WriteString("*None recorded.* No blockers, no dates, no open deviations, and nothing " +
			"unresolved in the architecture it touches. That is a real answer, not a missing " +
			"section — but it is worth a second look, because most work has at least a date.\n\n")
		return
	}
	for _, c := range list {
		b.WriteString("- " + c + "\n")
	}
	b.WriteString("\n")
}

// ── small helpers ────────────────────────────────────────────────────────────

// finished reports whether a status means the work is no longer coming.
// `dropped` counts: the decision not to do it is as final as doing it, and a
// dropped blocker is not blocking anything.
func finished(status string) bool { return status == "done" || status == "dropped" }

func statusLine(status string, pct int) string {
	if pct > 0 && pct < 100 {
		return fmt.Sprintf("`%s`, %d%% complete", status, pct)
	}
	return "`" + status + "`"
}

func milestoneLine(name, due string) string {
	if name == "" {
		return "*none* — this item is not under any milestone"
	}
	if due == "" {
		return name
	}
	return name + ", due " + due
}

func dateLine(start, due string) string {
	switch {
	case start == "" && due == "":
		return "*neither a start nor a due date is set*"
	case start == "":
		return "due " + due + ", no start date"
	case due == "":
		return "starts " + start + ", no due date"
	default:
		return "starts " + start + ", due " + due
	}
}

func dueClause(due string) string {
	if due == "" {
		return ""
	}
	return ", due " + due
}

func startClause(start string) string {
	if start == "" {
		return ""
	}
	return " It starts " + start + "."
}

func orNone(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return "*" + fallback + "*"
	}
	return s
}

// oneParagraph collapses stored prose onto one line, for the places where it
// appears inside a sentence. Used only there: intent and acceptance criteria
// are emitted verbatim, because their line breaks are somebody's structure.
func oneParagraph(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func slugish(s string) string {
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "milestone"
	}
	return out
}

func milestoneSlugs(m Milestone) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range m.Items {
		for _, s := range it.EntitySlugs {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	sort.Strings(out)
	return out
}
