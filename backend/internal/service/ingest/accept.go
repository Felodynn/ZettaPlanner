package ingest

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	ing "github.com/Felodynn/ZettaPlanner/backend/internal/domain/ingest"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
)

// Accept turns a reviewed ingest into diagram content.
//
// Extraction never writes to a diagram. It produces a reviewable payload, and
// this is the step that applies it — with a human's decisions folded in and one
// hard rule: **an element the model could not read cannot reach the canvas
// under an invented name.** Either somebody supplies the name or the element is
// dropped. There is no threshold and no heuristic, because a heuristic here
// picks confidently and wrongly.
//
// The application is additive (diagramsvc.Apply): a drawing is evidence that
// these things exist, never evidence that everything else does not.

// Resolution is a human's decision about one extracted element.
type Resolution struct {
	// Slug names the merged element. For an unreadable box this is the
	// positional placeholder the merge assigned, such as `unreadable-p2-1`.
	Slug string

	// Name is what it actually says. Supplying one makes an unreadable element
	// usable, and marks it human_edited rather than vlm_extracted — a human
	// read it, and the provenance should say so.
	Name string

	// Kind overrides the model's reading.
	Kind dg.NodeKind

	// Drop discards the element and every edge touching it.
	Drop bool
}

// AcceptInput is a reviewed ingest being applied.
type AcceptInput struct {
	// DiagramID applies to an existing diagram, additively. Zero creates one.
	DiagramID uuid.UUID

	// Name, Slug and Level describe the diagram to create. Name defaults to the
	// title the model read off the page; Level must be given, because a sketch
	// almost never says and guessing it wrong mislabels every diagram the
	// project has.
	Name  string
	Slug  string
	Level dg.Level

	Resolutions []Resolution

	// Summary is the revision summary. Defaults to naming the ingest.
	Summary string
}

// AcceptResult is what an accepted ingest produced.
type AcceptResult struct {
	Ingest      *ing.Ingest
	DiagramID   uuid.UUID
	DiagramSlug string
	Revision    int
	NodesAdded  int
	EdgesAdded  int
	Dropped     []string
	Notes       []*dg.Note
}

// Accept applies a reviewed ingest.
func (s *Service) Accept(ctx context.Context, id uuid.UUID, in AcceptInput, p policy.Principal) (*AcceptResult, error) {
	stored, err := s.ingests.WithPages(ctx, s.db.Pool, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireMember(ctx, s.db.Pool, p, stored.ProjectID); err != nil {
		return nil, fmt.Errorf("%w: ingest", domain.ErrNotFound)
	}
	if stored.Status != ing.StatusReview {
		return nil, &ing.TransitionError{From: stored.Status, To: ing.StatusAccepted}
	}
	if stored.Merged == nil {
		return nil, fmt.Errorf("%w: this ingest has no merged payload to accept", domain.ErrConflict)
	}

	plan, err := buildPlan(stored.Merged, in)
	if err != nil {
		return nil, err
	}

	apply := diagramsvc.ApplyInput{
		DiagramID: in.DiagramID,
		Summary:   plan.summary,
		Nodes:     plan.nodes,
		Edges:     plan.edges,
		Notes:     plan.notes,
		Action:    audit.ActionIngestAccepted,
		AuditAfter: map[string]any{
			"ingest_id": id.String(),
			"pages":     stored.Merged.PagesRead,
			"dropped":   plan.dropped,
		},
	}
	if in.DiagramID == uuid.Nil {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = strings.TrimSpace(stored.Merged.Title)
		}
		if name == "" {
			return nil, fmt.Errorf("%w: give the new diagram a name; the drawing did not carry a legible title", domain.ErrInvalid)
		}
		apply.Create = &diagramsvc.CreateInput{
			ProjectID: stored.ProjectID, Name: name, Slug: in.Slug, Level: plan.level,
		}
	}

	result := &AcceptResult{Dropped: plan.dropped}
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		applied, err := s.diagrams.ApplyInTx(ctx, tx, apply, p)
		if err != nil {
			return err
		}

		// The guard is the concurrency control: two reviewers accepting at once
		// cannot both produce a diagram, because the second UPDATE matches no
		// row and its whole transaction unwinds.
		ok, err := s.ingests.Accept(ctx, tx, id, applied.DiagramID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: this ingest was accepted by somebody else while you were reviewing it", domain.ErrConflict)
		}

		result.DiagramID = applied.DiagramID
		result.DiagramSlug = applied.Diagram.Slug
		result.Revision = applied.Revision
		result.NodesAdded = len(applied.Changes.NodesAdded)
		result.EdgesAdded = len(applied.Changes.EdgesAdded)
		result.Notes = applied.NotesRaised
		return nil
	})
	if err != nil {
		return nil, err
	}

	accepted, err := s.ingests.WithPages(ctx, s.db.Pool, id)
	if err != nil {
		return nil, err
	}
	result.Ingest = accepted
	return result, nil
}

// ── planning ─────────────────────────────────────────────────────────────────

type acceptPlan struct {
	level   dg.Level
	summary string
	nodes   []diagramsvc.NodeInput
	edges   []diagramsvc.EdgeInput
	notes   []diagramsvc.NoteInput
	dropped []string
}

// buildPlan folds a human's resolutions into the merged payload and refuses
// anything that would put an invented name on the canvas.
func buildPlan(m *ing.Merged, in AcceptInput) (*acceptPlan, error) {
	level := in.Level
	if level == "" {
		level = m.Level
	}
	if in.DiagramID == uuid.Nil {
		if err := dg.ValidateLevel(level); err != nil {
			return nil, err
		}
	}

	byOriginal := make(map[string]Resolution, len(in.Resolutions))
	for _, r := range in.Resolutions {
		slug := strings.TrimSpace(r.Slug)
		if slug == "" {
			return nil, fmt.Errorf("%w: every resolution must name the element it resolves, by slug", domain.ErrInvalid)
		}
		if m.NodeBySlug(slug) == nil {
			return nil, fmt.Errorf("%w: this ingest has no element %q. The slugs are the `slug` values in the merged payload",
				domain.ErrNotFound, slug)
		}
		if r.Kind != "" {
			if err := dg.ValidateNodeKind(r.Kind); err != nil {
				return nil, err
			}
		}
		byOriginal[slug] = r
	}

	plan := &acceptPlan{level: level, summary: strings.TrimSpace(in.Summary)}

	// The gate. Every unreadable element must have been decided on; the error
	// names each one and quotes what the model could make out, because that
	// partial reading is what a human needs in order to supply the real name.
	var unresolved []ing.MergedNode
	for _, n := range m.Illegible() {
		r, decided := byOriginal[n.Slug]
		if !decided || (!r.Drop && strings.TrimSpace(r.Name) == "") {
			unresolved = append(unresolved, n)
		}
	}
	if len(unresolved) > 0 {
		return nil, &ing.IllegibleError{Nodes: unresolved}
	}

	// slugFor maps a merged slug to the slug the diagram will use, which
	// changes when a human renames an unreadable box.
	slugFor := make(map[string]string, len(m.Nodes))
	dropped := map[string]bool{}
	// parentOf keeps each kept node's containment parent under its *merged*
	// slug, so containment can be wired once every rename is known without
	// searching back through the merged set for which node produced which slug.
	var parentOf []string
	var lowConfidence []string

	for _, n := range m.Nodes {
		r := byOriginal[n.Slug]
		if r.Drop {
			dropped[n.Slug] = true
			plan.dropped = append(plan.dropped, n.Slug)
			continue
		}

		node := diagramsvc.NodeInput{
			Slug: n.Slug, Kind: n.Kind, Name: n.Name, Summary: n.Summary,
			Confidence: confidencePtr(n.Confidence),
			Provenance: dg.ProvVLMExtracted,
		}
		if name := strings.TrimSpace(r.Name); name != "" {
			// A human read it. The provenance says so, and the confidence is
			// dropped entirely rather than carrying the model's uncertainty
			// about a name it no longer supplied.
			node.Name = name
			node.Slug = dg.Slugify(name)
			node.Provenance = dg.ProvHumanEdited
			node.Confidence = nil
			if node.Slug == "" {
				return nil, fmt.Errorf("%w: %q does not produce a usable identifier; give it a name with letters or digits in it",
					domain.ErrInvalid, name)
			}
		}
		if r.Kind != "" {
			node.Kind = r.Kind
			node.Provenance = dg.ProvHumanEdited
		}
		if n.Legible && n.Confidence < ing.ReviewThreshold && node.Provenance == dg.ProvVLMExtracted {
			lowConfidence = append(lowConfidence, fmt.Sprintf("%s (%.2f)", node.Slug, n.Confidence))
		}

		slugFor[n.Slug] = node.Slug
		plan.nodes = append(plan.nodes, node)
		parentOf = append(parentOf, n.Parent)
	}

	// Containment, once every final slug is known. A parent that was dropped
	// leaves its children in place rather than deleting them: dropping a
	// boundary is a statement about the boundary.
	for i, parentSlug := range parentOf {
		if parentSlug == "" || dropped[parentSlug] {
			continue
		}
		if parent, ok := slugFor[parentSlug]; ok {
			plan.nodes[i].Parent = parent
		}
	}

	for _, e := range m.Edges {
		switch {
		case e.Unresolved:
			// An arrow whose far end is nowhere cannot be drawn — there is no
			// second node to attach it to. Recorded as a Note rather than
			// discarded, because a dropped dependency is the error nobody
			// notices.
			plan.notes = append(plan.notes, diagramsvc.NoteInput{
				Kind: dg.NoteMissing,
				Body: fmt.Sprintf("An arrow read on %s could not be placed: %s. %s Nothing was invented to receive it — add the missing element if it belongs here.",
					pageWord(e.Pages), describeEdge(e), reasonSentence(e.ReviewReason)),
				SubjectSlug: firstKnown(slugFor, e.From, e.To),
			})
			continue
		case dropped[e.From] || dropped[e.To]:
			continue
		}

		from, okFrom := slugFor[e.From]
		to, okTo := slugFor[e.To]
		if !okFrom || !okTo {
			continue
		}
		plan.edges = append(plan.edges, diagramsvc.EdgeInput{
			From: from, To: to, Relation: e.Relation, Label: e.Label,
			Confidence: confidencePtr(e.Confidence), Provenance: dg.ProvVLMExtracted,
		})
	}

	// One note for the disagreements the merge refused to settle, one for the
	// low-confidence elements that were accepted anyway. Per-element notes
	// would flood a diagram with badges nobody reads; one note that lists them
	// is a thing somebody works through.
	for _, c := range m.Conflicts {
		plan.notes = append(plan.notes, diagramsvc.NoteInput{
			Kind: dg.NoteConflict, Body: c.Detail,
			SubjectSlug: slugFor[dg.Slugify(c.Name)],
		})
	}
	if len(lowConfidence) > 0 {
		sort.Strings(lowConfidence)
		plan.notes = append(plan.notes, diagramsvc.NoteInput{
			Kind: dg.NoteClarification,
			Body: fmt.Sprintf("%d element(s) were read with low confidence and accepted as they stand: %s. "+
				"The confidence is on each node; check them against the drawing before anybody plans against them.",
				len(lowConfidence), strings.Join(lowConfidence, ", ")),
		})
	}
	if len(m.PagesFailed) > 0 {
		plan.notes = append(plan.notes, diagramsvc.NoteInput{
			Kind: dg.NoteMissing,
			Body: fmt.Sprintf("%s of this drawing could not be read at all, so whatever was on it is not here. "+
				"Retry the page or add its contents by hand.", pageWord(m.PagesFailed)),
		})
	}

	if plan.summary == "" {
		plan.summary = fmt.Sprintf("Accepted from a drawing: %d node(s), %d edge(s) across %s",
			len(plan.nodes), len(plan.edges), pageWord(m.PagesRead))
	}
	sort.Strings(plan.dropped)
	return plan, nil
}

func firstKnown(slugFor map[string]string, candidates ...string) string {
	for _, c := range candidates {
		if s, ok := slugFor[c]; ok {
			return s
		}
	}
	return ""
}

func describeEdge(e ing.MergedEdge) string {
	label := ""
	if e.Label != "" {
		label = fmt.Sprintf(" labelled %q", e.Label)
	}
	return fmt.Sprintf("%s %s %s%s", endpointName(e.From), e.Relation, endpointName(e.To), label)
}

// endpointName renders an edge end for a human. An unresolved end holds the
// text the model read rather than a slug, and it may be anything at all —
// including nothing, when the arrow pointed at a question mark.
func endpointName(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(nothing legible)"
	}
	return s
}

func reasonSentence(reason string) string {
	if reason == "" {
		return ""
	}
	r := []rune(reason)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r) + "."
}

func pageWord(pages []int) string {
	if len(pages) == 0 {
		return "no page"
	}
	parts := make([]string, 0, len(pages))
	for _, p := range pages {
		parts = append(parts, fmt.Sprintf("%d", p+1))
	}
	if len(parts) == 1 {
		return "page " + parts[0]
	}
	return "pages " + strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

func confidencePtr(v float32) *float32 {
	c := v
	return &c
}
