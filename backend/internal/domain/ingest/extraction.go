package ingest

import (
	"fmt"
	"sort"
	"strings"

	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
)

// ReviewThreshold is the confidence below which an element is surfaced for
// review rather than passed through quietly.
//
// 0.6 rather than 0.5: the midpoint is where a model puts things it is
// genuinely unsure about, and a threshold at the midpoint sorts exactly those
// into "fine". The number is a product decision, not a measurement, and it is
// named here so that when dogfood says it is wrong there is one place to change.
const ReviewThreshold float32 = 0.6

// PageExtraction is exactly what the model returned for one page.
//
// Stored verbatim in ingest_pages.extracted and never rewritten. The merge step
// reads it; nothing edits it. When an extraction turns out to be wrong, this is
// the evidence — re-running against the image costs tokens and does not
// reproduce.
type PageExtraction struct {
	// Title is whatever the page called itself, if anything. Used as a
	// suggested diagram name, never as an identity.
	Title string `json:"title,omitempty"`

	// Level is the model's reading of the diagram's altitude. Advisory: the
	// human picks the real one on accept, because a sketch rarely says.
	Level dg.Level `json:"level,omitempty"`

	Nodes []ExtractedNode `json:"nodes"`
	Edges []ExtractedEdge `json:"edges"`

	// Notes is anything the model wants to say that is not a node or an edge:
	// a legend, a caption, a question mark somebody drew. Carried into review
	// so a human sees what the model saw and chose not to structure.
	Notes []string `json:"notes,omitempty"`
}

// ExtractedNode is one box the model read.
type ExtractedNode struct {
	Name string      `json:"name"`
	Kind dg.NodeKind `json:"kind"`

	// Summary is any explanatory text written inside or beside the box.
	Summary string `json:"summary,omitempty"`

	// Parent names the containing boundary or system, by the name the model
	// gave it on this same page.
	Parent string `json:"parent,omitempty"`

	// Confidence is the model's own certainty, 0.0–1.0.
	Confidence float32 `json:"confidence"`

	// Legible is false when the model could not actually read the text.
	//
	// This is the field that stops an invented label. When it is false the
	// element cannot reach a diagram until a human supplies the name — no
	// threshold, no heuristic, a hard gate on accept. A verifier with only
	// pass/fail picks wrong here, every time, in the confident direction.
	Legible bool `json:"legible"`

	// Verbatim is whatever characters the model could make out, however
	// partial. On an illegible element this is the only real information there
	// is, and it is what a human uses to supply the name.
	Verbatim string `json:"verbatim,omitempty"`
}

// ExtractedEdge is one arrow the model read.
type ExtractedEdge struct {
	From     string      `json:"from"`
	To       string      `json:"to"`
	Relation dg.Relation `json:"relation"`
	Label    string      `json:"label,omitempty"`

	Confidence float32 `json:"confidence"`

	// ContinuesOffPage marks an arrow that leaves the sheet. The model records
	// where it was going as best it can read it, and the merge step tries to
	// find that target on another page. This is asked for explicitly because a
	// model told to extract "the diagram" will otherwise quietly drop the
	// half-arrow at the edge of the paper, and a dropped dependency is the one
	// error nobody notices.
	ContinuesOffPage bool `json:"continues_off_page,omitempty"`
}

// ── merge ────────────────────────────────────────────────────────────────────

// Merged is the reconciliation of every page, and the record of what it refused
// to decide.
//
// Deterministic, not a second model call (D37). The judgements a merge has to
// make are exactly the ones §3.4 says must never be automatic; a model asked to
// make them produces a confident answer over data that is already uncertain,
// and the second failure compounds the first invisibly.
type Merged struct {
	Title string   `json:"title,omitempty"`
	Level dg.Level `json:"level,omitempty"`

	Nodes []MergedNode `json:"nodes"`
	Edges []MergedEdge `json:"edges"`

	// Conflicts are the things two pages disagreed about. Recorded, never
	// resolved: only somebody who knows the system can say.
	Conflicts []Conflict `json:"conflicts,omitempty"`

	Notes []string `json:"notes,omitempty"`

	// PagesRead and PagesFailed say what this merge is a merge of, so a review
	// screen can say "four of five pages" without recounting.
	PagesRead   []int `json:"pages_read"`
	PagesFailed []int `json:"pages_failed,omitempty"`
}

// MergedNode is one node as it stands after reconciliation.
type MergedNode struct {
	// Slug is the identity the accept step uses to resolve or create a catalog
	// entity. Derived from the name; empty while the name is unusable.
	Slug string      `json:"slug"`
	Name string      `json:"name"`
	Kind dg.NodeKind `json:"kind"`

	Summary string `json:"summary,omitempty"`
	Parent  string `json:"parent,omitempty"`

	// Confidence is the weakest reading across the pages this node appeared
	// on. The weakest, not the average: a node read clearly on page 1 and
	// barely on page 3 is a node worth a second look, and averaging hides that.
	Confidence float32 `json:"confidence"`

	Legible  bool   `json:"legible"`
	Verbatim string `json:"verbatim,omitempty"`

	// Pages lists every page this node was read on.
	Pages []int `json:"pages"`

	// NeedsReview and ReviewReason are computed, never stored as a judgement.
	NeedsReview  bool   `json:"needs_review"`
	ReviewReason string `json:"review_reason,omitempty"`
}

// MergedEdge is one edge after reconciliation.
type MergedEdge struct {
	From     string      `json:"from"`
	To       string      `json:"to"`
	Relation dg.Relation `json:"relation"`
	Label    string      `json:"label,omitempty"`

	Confidence float32 `json:"confidence"`
	Pages      []int   `json:"pages"`

	// CrossPage marks an edge whose two ends were read on different pages.
	// This is the merge step's actual work, and it is flagged rather than
	// smoothed over: a human reviewing an ingest should look hardest here.
	CrossPage bool `json:"cross_page,omitempty"`

	// Unresolved marks an arrow that ran off the page and found nothing to
	// attach to. Kept, with whatever the model read of its destination, rather
	// than dropped — a dropped dependency is the error nobody notices. It
	// cannot be accepted into a diagram; a human resolves it or discards it.
	Unresolved bool `json:"unresolved,omitempty"`

	NeedsReview  bool   `json:"needs_review"`
	ReviewReason string `json:"review_reason,omitempty"`
}

// Conflict is a disagreement between pages that the merge declined to settle.
type Conflict struct {
	Kind   string `json:"kind"`   // node_kind_disagreement | relation_disagreement
	Name   string `json:"name"`   // the thing being disagreed about
	Detail string `json:"detail"` // written to be read by a human, and by a model
	Pages  []int  `json:"pages"`
}

// ReviewCounts summarises a merged payload for a review screen.
type ReviewCounts struct {
	Nodes         int `json:"nodes"`
	Edges         int `json:"edges"`
	NeedsReview   int `json:"needs_review"`
	Illegible     int `json:"illegible"`
	Unresolved    int `json:"unresolved"`
	Conflicts     int `json:"conflicts"`
	LowConfidence int `json:"low_confidence"`
}

// Counts tallies what a human is being asked to look at.
func (m *Merged) Counts() ReviewCounts {
	c := ReviewCounts{Nodes: len(m.Nodes), Edges: len(m.Edges), Conflicts: len(m.Conflicts)}
	for _, n := range m.Nodes {
		if n.NeedsReview {
			c.NeedsReview++
		}
		if !n.Legible {
			c.Illegible++
		}
		if n.Legible && n.Confidence < ReviewThreshold {
			c.LowConfidence++
		}
	}
	for _, e := range m.Edges {
		if e.NeedsReview {
			c.NeedsReview++
		}
		if e.Unresolved {
			c.Unresolved++
		}
		if !e.Unresolved && e.Confidence < ReviewThreshold {
			c.LowConfidence++
		}
	}
	return c
}

// Illegible returns the nodes a human must name before this ingest can be
// accepted.
func (m *Merged) Illegible() []MergedNode {
	var out []MergedNode
	for _, n := range m.Nodes {
		if !n.Legible {
			out = append(out, n)
		}
	}
	return out
}

// NodeBySlug finds a merged node.
func (m *Merged) NodeBySlug(slug string) *MergedNode {
	for i := range m.Nodes {
		if m.Nodes[i].Slug == slug {
			return &m.Nodes[i]
		}
	}
	return nil
}

// ── the gate ─────────────────────────────────────────────────────────────────

// IllegibleError refuses an accept that would put an invented label on the
// canvas. It names every element and quotes whatever the model could make out,
// because that partial reading is what a human needs to supply the real name.
type IllegibleError struct {
	Nodes []MergedNode
}

func (e *IllegibleError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d element(s) could not be read from the drawing, and this ingest will not invent names for them. "+
		"Give each one a name, or drop it:\n", len(e.Nodes))
	for _, n := range e.Nodes {
		if n.Verbatim != "" {
			fmt.Fprintf(&b, "  - %s — the model made out %q\n", n.Slug, n.Verbatim)
			continue
		}
		fmt.Fprintf(&b, "  - %s — nothing legible at all\n", n.Slug)
	}
	b.WriteString("Send each as a `resolutions` entry with a name, or with drop: true.")
	return b.String()
}

// Slugs lists the unreadable elements' placeholder identities.
func (e *IllegibleError) Slugs() []string {
	out := make([]string, 0, len(e.Nodes))
	for _, n := range e.Nodes {
		out = append(out, n.Slug)
	}
	sort.Strings(out)
	return out
}
