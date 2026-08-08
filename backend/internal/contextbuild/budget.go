package contextbuild

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// DefaultBudget is the ceiling when the caller names no model.
//
// Deliberately small. `assembly_budget` in models.yaml is what a real turn
// uses; this is what `context show` falls back to, and a fallback that quietly
// allows a 200,000-token context would hide exactly the problem the command
// exists to surface.
const DefaultBudget = 24000

// Budget is the ceiling assembly targets, and the model it was derived from.
//
// Not `num_ctx`. glm-5.2:cloud advertises a million-token window; filling it is
// neither affordable nor good for recall, and the model that has to find one
// fact in 900,000 tokens of plan is not the model anyone wants answering.
// models.yaml carries `assembly_budget` separately for that reason.
type Budget struct {
	Tokens int
	Model  string
}

// Drop is one thing left out, and why.
//
// Every drop is rendered into the output the model reads. A context that
// quietly lost the deviation explaining why an item is blocked produces a
// confidently wrong answer with nothing in the transcript to explain it.
type Drop struct {
	Layer Layer
	// What is the thing dropped, in the words a reader would use: "4 of 9
	// retrieved passages", "subtask lists on 3 expanded items".
	What string
	// Why is the reason. Almost always the budget, but "no embedding model is
	// configured" is a drop too, and the more important one to say out loud.
	Why string
	// Tokens is the estimated saving, or 0 when the drop was not about size.
	Tokens int
}

// EstimateTokens approximates the token count of a string.
//
// **An estimate, and it says so everywhere it is printed.** A real count needs
// the target model's tokenizer, which lives on the model server; asking for it
// costs a round trip per assembly and, on a cloud model, tokens. The heuristic
// is four characters per token adjusted for whitespace, which runs about 10%
// high on the structured text this package emits — high is the safe direction,
// because an underestimate is a context that overflows at the model rather than
// at the budget check.
//
// The number's job is to answer "which layer is eating the budget", and for
// that a consistent estimate is worth as much as an exact one.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	var chars, breaks int
	for _, r := range s {
		if unicode.IsSpace(r) {
			breaks++
			continue
		}
		chars++
	}
	// Whitespace is close to free in BPE — runs of it merge into the token
	// beside them — so it is counted at a quarter weight rather than ignored,
	// which would underestimate deeply indented output.
	n := (chars*4 + breaks) / 15
	if n < 1 {
		return 1
	}
	return n
}

// renderDrops produces the OMITTED section, or "" when nothing was left out.
func renderDrops(drops []Drop) string {
	if len(drops) == 0 {
		return ""
	}
	// Sorted so two assemblies that dropped the same things say so in the same
	// order.
	ordered := append([]Drop(nil), drops...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Layer != ordered[j].Layer {
			return ordered[i].Layer < ordered[j].Layer
		}
		return ordered[i].What < ordered[j].What
	})

	var b strings.Builder
	b.WriteString("## OMITTED\n")
	b.WriteString("# Left out of this context. If the answer depends on any of it, say so rather\n")
	b.WriteString("# than guessing — the omission is the harness's doing, not the project's.\n")
	for _, d := range ordered {
		b.WriteString("- [" + string(d.Layer) + "] " + d.What + " — " + d.Why)
		if d.Tokens > 0 {
			b.WriteString(" (~" + itoa(d.Tokens) + " tokens)")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func itoa(n int) string { return strconv.Itoa(n) }
