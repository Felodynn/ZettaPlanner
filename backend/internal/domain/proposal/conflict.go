package proposal

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
)

// A proposal's premise, and what happens when it has gone.
//
// Every change records the state it was raised against in `before`. Approving
// one whose premise has moved is how a plan silently reverts to something
// nobody chose: somebody pushes a date out, an hour later a proposal written
// against the old date is approved, and the push is undone by a click that said
// nothing about dates. §5.1's answer is that the **whole** proposal becomes
// `superseded` and the conflict is reported — never partially applied, never
// auto-merged, because merging is the judgement the human was being asked to
// make.

// Conflict is one recorded premise that no longer holds.
type Conflict struct {
	// Seq is the change's position in the proposal, so a reviewer can find it.
	Seq    int
	Target ChangeTarget
	// Element is what a human calls the thing: `ZP-14`, `M1: Beta`, `redis-cache`.
	Element string
	// Field is the field that moved. Empty when the whole target is the problem
	// — deleted, or gone.
	Field string
	// Expected is what the proposal was raised against; Actual is what is there
	// now. Both rendered as the reviewer would read them, not as ids.
	Expected string
	Actual   string
	// Reason is the sentence shown when Field is empty.
	Reason string
}

// String renders one conflict for a human.
func (c Conflict) String() string {
	if c.Field == "" {
		return fmt.Sprintf("change %d: %s %s", c.Seq, c.Element, c.Reason)
	}
	return fmt.Sprintf("change %d: %s %s was %q when this was raised and is %q now",
		c.Seq, c.Element, c.Field, c.Expected, c.Actual)
}

// ConflictError is a proposal whose premise has moved.
//
// It maps to `409 conflict` and carries every conflict rather than the first:
// a reviewer deciding whether to re-raise needs to know how far the world has
// moved, and being told one field at a time turns that into a guessing game.
type ConflictError struct {
	ProposalID string
	Conflicts  []Conflict
}

func (e *ConflictError) Error() string {
	var b strings.Builder
	b.WriteString("This proposal was raised against a state that has since moved, so it has been marked " +
		"superseded and nothing was applied. ")
	switch len(e.Conflicts) {
	case 0:
		b.WriteString("No conflict was recorded, which is itself a bug worth reporting.")
	case 1:
		b.WriteString("The conflict is:\n\n  " + e.Conflicts[0].String())
	default:
		fmt.Fprintf(&b, "The %d conflicts are:\n", len(e.Conflicts))
		for _, c := range e.Conflicts {
			b.WriteString("\n  " + c.String())
		}
	}
	b.WriteString("\n\nNothing was merged automatically: reconciling a proposal with what has happened since " +
		"is the judgement you were being asked to make. Raise it again against the current state if it still holds.")
	return b.String()
}

func (e *ConflictError) Is(target error) bool { return target == domain.ErrConflict }

// Details is the error envelope's `details` object (§7.3).
func (e *ConflictError) Details() map[string]any {
	out := make([]map[string]any, 0, len(e.Conflicts))
	for _, c := range e.Conflicts {
		entry := map[string]any{"seq": c.Seq, "target": string(c.Target), "element": c.Element}
		if c.Field != "" {
			entry["field"] = c.Field
			entry["expected"] = c.Expected
			entry["actual"] = c.Actual
		} else {
			entry["reason"] = c.Reason
		}
		out = append(out, entry)
	}
	return map[string]any{"proposal_id": e.ProposalID, "conflicts": out}
}

// CompareSnapshot reports every field this change would write that the current
// state no longer holds.
//
// **The premise is what the change writes, not everything it happened to note.**
// The propose tools record a fixed snapshot — ref, title, status, priority, both
// dates — regardless of what the change is about, so comparing all of it would
// mean a developer ticking a status supersedes every pending proposal about that
// item. In a live project that fires constantly, and a conflict that fires
// constantly is one people learn to click past, which costs more than the case
// it was guarding.
//
// What it still catches is the case worth catching: a proposal that would set a
// due date, approved after somebody else already moved that same due date,
// silently reverts their decision. That is a field this change writes, so it is
// a conflict. Existence is checked separately by the caller and is never
// narrowed — a proposal about something that has been deleted is stale whatever
// it was going to write.
//
// Recorded as D57, and worth revisiting in dogfood: the broader reading is the
// safer one on paper, and the question is whether the noise is real.
func CompareSnapshot(seq int, target ChangeTarget, element string, before, writes, current map[string]any) []Conflict {
	if len(before) == 0 {
		return nil
	}
	fields := make([]string, 0, len(before))
	for f := range before {
		if _, written := writes[f]; !written {
			continue
		}
		fields = append(fields, f)
	}
	// Sorted so the same divergence always reports in the same order.
	sort.Strings(fields)

	var out []Conflict
	for _, field := range fields {
		was := renderValue(before[field])
		now, known := current[field]
		if !known {
			continue
		}
		if is := renderValue(now); is != was {
			out = append(out, Conflict{
				Seq: seq, Target: target, Element: element,
				Field: field, Expected: was, Actual: is,
			})
		}
	}
	return out
}

// renderValue normalises a JSONB round-trip.
//
// `before` comes back from Postgres through encoding/json, where every number
// is a float64 and every id is a string; the current state is built in Go from
// typed fields. Comparing them raw reports a conflict on `seq` between 3 and
// 3.0, which is a stale proposal nobody can explain.
func renderValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}
