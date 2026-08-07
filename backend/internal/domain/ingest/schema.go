package ingest

import (
	"encoding/json"
	"fmt"
	"strings"

	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
)

// ExtractionSchema is the JSON Schema handed to Ollama's `format` field.
//
// **Schema-constrained, not asked-for-JSON-in-prose.** In LumixAgents Phase 4 a
// model asked for JSON in prose left the content field empty and put everything
// in its reasoning channel, failing roughly half of all calls. Constraining the
// format removed the failure mode instead of parsing around it.
//
// Every enum here is generated from the vocabulary in internal/domain/diagram,
// so the set the model is allowed to emit and the set the database accepts
// cannot drift apart. Adding a node kind in one place and forgetting the other
// would otherwise produce extractions that validate at the model and fail at
// the insert.
func ExtractionSchema() json.RawMessage {
	kinds := make([]string, 0, len(dg.NodeKinds()))
	for _, k := range dg.NodeKinds() {
		kinds = append(kinds, string(k))
	}
	relations := make([]string, 0, len(dg.Relations()))
	for _, r := range dg.Relations() {
		relations = append(relations, string(r))
	}
	levels := make([]string, 0, len(dg.Levels()))
	for _, l := range dg.Levels() {
		levels = append(levels, string(l))
	}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{
				"type":        "string",
				"description": "The title written on the page, verbatim. Empty if the page has none.",
			},
			"level": map[string]any{
				"type":        "string",
				"enum":        levels,
				"description": "Your reading of the diagram's altitude. Advisory only.",
			},
			"nodes": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "The label written in or on the box, verbatim. If you cannot read it, set legible to false and put a positional description here instead, such as \"box below the queue\".",
						},
						"kind": map[string]any{
							"type": "string",
							"enum": kinds,
						},
						"summary": map[string]any{
							"type":        "string",
							"description": "Any explanatory text written inside or beside the box.",
						},
						"parent": map[string]any{
							"type":        "string",
							"description": "The name of the boundary or system this box is drawn inside, exactly as you named it in this same list. Empty if it is not inside one.",
						},
						"confidence": map[string]any{
							"type":        "number",
							"minimum":     0,
							"maximum":     1,
							"description": "How sure you are of this reading. Use the whole range. 0.9 is a clearly printed label; 0.4 is a guess from context; below 0.3 means you are largely inferring.",
						},
						"legible": map[string]any{
							"type":        "boolean",
							"description": "True only if you actually read the text. False if you are inferring the name from position, from an arrow, or from what would make sense.",
						},
						"verbatim": map[string]any{
							"type":        "string",
							"description": "Whatever characters you could make out, however partial or wrong-looking. Required when legible is false. Do not clean it up.",
						},
					},
					"required": []string{"name", "kind", "confidence", "legible"},
				},
			},
			"edges": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from": map[string]any{
							"type":        "string",
							"description": "The name of the source box, exactly as you wrote it in nodes.",
						},
						"to": map[string]any{
							"type":        "string",
							"description": "The name of the target box. If the arrow leaves the page, write what you can read of its destination and set continues_off_page.",
						},
						"relation": map[string]any{
							"type": "string",
							"enum": relations,
						},
						"label": map[string]any{
							"type":        "string",
							"description": "The text written on the arrow, verbatim.",
						},
						"confidence": map[string]any{
							"type":    "number",
							"minimum": 0,
							"maximum": 1,
						},
						"continues_off_page": map[string]any{
							"type":        "boolean",
							"description": "True when the arrow runs off the edge of the sheet. Record it anyway; another page may hold the other end.",
						},
					},
					"required": []string{"from", "to", "relation", "confidence"},
				},
			},
			"notes": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Anything on the page that is not a box or an arrow: a legend, a caption, a question mark somebody drew, a crossed-out section.",
			},
		},
		"required": []string{"nodes", "edges"},
	}

	// Marshalled here rather than kept as a literal string so a change to the
	// vocabulary is a compile-and-run away from being reflected, and so the
	// bytes are valid JSON by construction.
	raw, err := json.Marshal(schema)
	if err != nil {
		// Unreachable: the value is built from strings, maps and slices.
		panic("ingest: extraction schema is not marshalable: " + err.Error())
	}
	return raw
}

// ExtractionPrompt is the system prompt for one page.
//
// It teaches the closed vocabulary **with meanings**, for the same reason the
// validation errors do: a list of bare words produces a second wrong answer.
// And it says, in the plainest terms available, that an unreadable box is to be
// reported as unreadable. That instruction is load-bearing — a vision model's
// default behaviour on a smudged label is to produce something plausible, and
// something plausible is worse than nothing because nobody checks it.
func ExtractionPrompt(pageIdx, pageCount int) string {
	var b strings.Builder

	b.WriteString("You are reading one page of a hand-drawn or printed software architecture diagram ")
	b.WriteString("and turning it into structured data.\n\n")

	if pageCount > 1 {
		fmt.Fprintf(&b, "This is page %d of %d. Extract ONLY what is on this page. ", pageIdx+1, pageCount)
		b.WriteString("Do not guess at what the other pages contain, and do not invent boxes to give an arrow somewhere to land. ")
		b.WriteString("An arrow that leaves the edge of the sheet is recorded with continues_off_page set to true and whatever you can read of its destination; the pages are reconciled afterwards.\n\n")
	} else {
		b.WriteString("Extract only what is on this page.\n\n")
	}

	b.WriteString("The photograph may be poor: taken at an angle, folded, badly lit, or out of focus. ")
	b.WriteString("That is expected and it is not a reason to fill in the gaps.\n\n")

	b.WriteString("THE MOST IMPORTANT RULE: never invent a label.\n")
	b.WriteString("If you cannot actually read what is written in a box, set legible to false, put whatever ")
	b.WriteString("characters you could make out in verbatim however partial they look, put a positional ")
	b.WriteString("description in name such as \"box left of the database\", and give it a low confidence. ")
	b.WriteString("A box reported as unreadable is useful — a human will read it. ")
	b.WriteString("A plausible name you inferred is worse than nothing, because nobody will check it.\n\n")

	b.WriteString("Use the confidence range honestly and across its whole width. ")
	b.WriteString("0.9 means clearly printed and unambiguous. 0.5 means you are fairly sure. ")
	b.WriteString("0.3 means you are reading it from context rather than from the page. ")
	b.WriteString("Everything at 0.9 tells a reviewer nothing.\n\n")

	b.WriteString("The vocabulary is closed. These are all of the node kinds:\n")
	for _, k := range dg.NodeKinds() {
		fmt.Fprintf(&b, "  %-16s %s\n", k, dg.NodeKindMeaning(k))
	}
	b.WriteString("\nPick the closest kind rather than inventing one. Use `component` for a module inside a service ")
	b.WriteString("and `service` for something independently deployable; when the drawing does not say, `service` ")
	b.WriteString("is the safer reading at a whiteboard's level of detail.\n\n")

	b.WriteString("These are all of the edge relations:\n")
	for _, r := range dg.Relations() {
		fmt.Fprintf(&b, "  %-16s %s\n", r, dg.RelationMeaning(r))
	}
	b.WriteString("\nUse data_flow when nothing more specific is true, rather than inventing a relation. ")
	b.WriteString("An unlabelled arrow between two services is usually `calls`; an arrow into a datastore is ")
	b.WriteString("`writes_to` and out of one is `reads_from`. If the arrowhead is ambiguous, say so with a low confidence ")
	b.WriteString("rather than picking a direction silently.\n\n")

	b.WriteString("Containment: when a box is drawn inside another box, name the outer one in the inner one's parent field. ")
	b.WriteString("Only a boundary or a system may contain other boxes. Do not also emit a `contains` edge for it.\n\n")

	b.WriteString("Every box on the page gets an entry, including the ones you cannot read. ")
	b.WriteString("Every arrow gets an entry, including the ones that run off the page.\n\n")

	b.WriteString(outputContract)
	return b.String()
}

// outputContract restates the response shape in the prompt.
//
// Redundant when the server enforces `format`, and load-bearing when it does
// not. Ollama Cloud silently discards the field (D38, measured): a cloud model
// answers in prose with the JSON in a fenced block, invents its own key names,
// and omits whole arrays. Without this paragraph the first cloud extraction
// returned three arrows, zero boxes, and a key called `boxes`.
//
// The two keys are named explicitly because that is exactly what went wrong.
const outputContract = `Answer with one JSON object and nothing else. No prose before it, no explanation after it.

It has exactly two required keys:
  "nodes"  an array with one entry per box on the page — never omit it, never rename it, never call it "boxes"
  "edges"  an array with one entry per arrow

and two optional ones, "title" and "level".

Each entry in "nodes" has: name, kind, confidence, legible, and optionally summary, parent, verbatim.
Each entry in "edges" has: from, to, relation, confidence, and optionally label, continues_off_page.

"from" and "to" are the names of boxes, written exactly as they appear in "nodes". They are never null:
an arrow whose far end is off the page names whatever you can read of its destination and sets
continues_off_page. If a page has no boxes at all, return "nodes": [] — an empty array, not a missing key.`
