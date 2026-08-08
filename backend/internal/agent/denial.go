package agent

import (
	"fmt"
	"sort"
	"strings"
)

// Denials are a prompt layer (§7.3, D34).
//
// A refusal that leads with the reason gets prose back; one that leads with the
// action gets the action. Every denial here therefore has the same shape: name
// the boundary, name the route, in that order — and on a repeat, say so and
// tell it to stop rephrasing.
//
// The escalation matters more than it looks. A model that is refused once will
// try the same thing worded differently, and a harness that answers identically
// invites it to keep going until it hits MaxTurns. Counting the refusals and
// saying the count out loud is what converts a loop into a decision.

// denialTracker counts refusals per tool within one turn.
type denialTracker struct {
	byTool map[string]int
	total  int
}

func newDenialTracker() *denialTracker { return &denialTracker{byTool: map[string]int{}} }

// record notes a refusal and returns how many times this tool has now been
// refused in this turn.
func (d *denialTracker) record(tool string) int {
	d.byTool[tool]++
	d.total++
	return d.byTool[tool]
}

// escalation is the sentence appended to a repeated refusal.
//
// Three strikes ends the turn rather than continuing to refuse: at that point
// the model is not going to find a way through, and every further step costs
// tokens to produce the same answer.
func escalation(tool string, count int) string {
	switch {
	case count <= 1:
		return ""
	case count == 2:
		return fmt.Sprintf("\n\nThis is the second time `%s` has been refused in this turn. "+
			"Rephrasing will not change the answer — the boundary is checked against the ownership table, "+
			"not against how the request is worded. Take the route named above, or call "+
			"`handoff_to_human` and say what you were trying to do.", tool)
	default:
		return fmt.Sprintf("\n\nThis is refusal %d for `%s` in this turn. Stop trying. "+
			"Call `handoff_to_human` with what you were attempting and why you think it is needed, "+
			"or call `finish` if the rest of the work is done.", count, tool)
	}
}

// tooManyDenials is the ceiling on refusals in one turn.
//
// Not a limit on the model's persistence for its own sake: a turn that spends
// its budget being told no has produced nothing, and ending it with a clear
// `no_output` is more useful to whoever reads it later than forty identical
// refusals in the trace.
const tooManyDenials = 6

// unknownTool is the message for a tool the model invented.
//
// It names what is available rather than only what is not, because the most
// common invented tool is a real capability under the wrong name — `update_plan`
// for `propose_plan_change` — and listing the inventory turns a dead end into a
// correction.
func unknownTool(name string, available []string) string {
	sorted := append([]string(nil), available...)
	sort.Strings(sorted)

	var b strings.Builder
	fmt.Fprintf(&b, "`%s` is not a tool that exists.", name)
	if route := nearestRoute(name); route != "" {
		fmt.Fprintf(&b, " %s", route)
	}
	fmt.Fprintf(&b, "\n\nThe tools available in this turn are:\n  %s", strings.Join(sorted, "\n  "))
	b.WriteString("\n\nCall one of those. Inventing a name does not create the capability, " +
		"and the ones that are missing are missing on purpose.")
	return b.String()
}

// nearestRoute maps the plausible inventions onto the real route.
//
// Hand-written rather than fuzzy-matched: these are the specific wrong guesses
// the tool inventory invites — a model that wants to change a manager-owned
// field reaches for `update_plan` — and naming the route for each is worth more
// than a general-purpose "did you mean".
func nearestRoute(name string) string {
	switch normaliseToolName(name) {
	case "update_plan", "update_work_item", "update_item", "set_due_date", "update_milestone",
		"assign_item", "set_priority", "set_assignee", "move_milestone", "create_work_item",
		"add_work_item", "delete_work_item", "set_dependencies":
		return "Changes to manager-owned fields — titles, dates, assignees, priorities, milestones, " +
			"dependencies, entity refs — are raised as proposals. Call `propose_plan_change`."
	case "update_diagram", "add_node", "add_edge", "create_diagram", "edit_diagram", "delete_node":
		return "The architecture graph is manager-owned. Call `propose_diagram_change`."
	case "update_status", "set_status", "set_progress", "mark_done", "mark_in_progress":
		return "Status and progress on an item assigned to the requesting user are a direct write. " +
			"Call `update_dev_field`."
	case "search", "search_plan", "query", "semantic_search":
		return "Retrieval over worklogs and messages is `search_worklogs`. Everything structured — " +
			"items, notes, deviations, the graph — is already in the context or is fetched by its own tool."
	case "ask_user", "ask", "clarify", "request_input":
		return "There is no way to ask a question mid-turn. Call `handoff_to_human` with the question."
	}
	return ""
}

func normaliseToolName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	return s
}
