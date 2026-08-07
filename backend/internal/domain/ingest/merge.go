package ingest

import (
	"fmt"
	"sort"
	"strings"

	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
)

// Merge reconciles the per-page extractions into one reviewable payload.
//
// **Deterministic code, not a second model call** (D37). The judgements this
// step has to make — is the box on page 3 the same box as the one on page 1,
// where does the arrow that ran off the sheet actually go — are exactly the
// judgements §3.4 says must never be made automatically. A model asked to make
// them returns a confident answer over data that is already uncertain, and the
// second failure compounds the first with nothing left to inspect. So every
// judgement this function declines to make is recorded, and every judgement it
// does make is flagged with the pages it drew on.
//
// It is pure: no database, no model, no clock. That is what makes the rules
// testable against a hand-written five-page disagreement rather than against a
// photograph.
func Merge(pages []*Page) *Merged {
	m := &Merged{Nodes: []MergedNode{}, Edges: []MergedEdge{}}

	// Read pages in index order so the output does not depend on the order the
	// extractions happened to finish in. Byte-determinism is the same
	// discipline the serializer holds to, and for the same reason: a payload
	// that shuffles between runs cannot be diffed or reviewed twice.
	ordered := make([]*Page, len(pages))
	copy(ordered, pages)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Idx < ordered[j].Idx })

	var read []*Page
	for _, p := range ordered {
		switch p.Status {
		case PageExtracted:
			if p.Extracted != nil {
				read = append(read, p)
				m.PagesRead = append(m.PagesRead, p.Idx)
			}
		case PageFailed:
			m.PagesFailed = append(m.PagesFailed, p.Idx)
		}
	}
	if m.PagesRead == nil {
		m.PagesRead = []int{}
	}

	m.Title, m.Level = mergeHeader(read)
	for _, p := range read {
		m.Notes = append(m.Notes, p.Extracted.Notes...)
	}

	index := mergeNodes(m, read)
	mergeEdges(m, read, index)

	sort.Slice(m.Conflicts, func(i, j int) bool {
		if m.Conflicts[i].Name != m.Conflicts[j].Name {
			return m.Conflicts[i].Name < m.Conflicts[j].Name
		}
		return m.Conflicts[i].Kind < m.Conflicts[j].Kind
	})
	return m
}

// nodeIndex maps a page-local name onto the merged node it became.
type nodeIndex struct {
	// bySlug is the merged set, keyed by the identity accept will use.
	bySlug map[string]*MergedNode
	// byName resolves a name as written on a page to a merged slug. Keyed by
	// the normalised form, which is what "the same box drawn twice" compares
	// equal under.
	byName map[string]string
	// perPage records which slugs were read on which page, so an edge can be
	// told whether it crossed one.
	perPage map[int]map[string]bool
}

// mergeNodes folds every page's boxes into one set.
//
// Two boxes are the same box when their names normalise equal. They are not
// merged on similarity, on prefix, or on anything else — that is entity
// resolution's job, it happens against the project catalog on accept, and it
// records a Note rather than merging (§3.4).
func mergeNodes(m *Merged, pages []*Page) *nodeIndex {
	idx := &nodeIndex{
		bySlug:  map[string]*MergedNode{},
		byName:  map[string]string{},
		perPage: map[int]map[string]bool{},
	}

	// order preserves first-seen sequence so the result is stable; the final
	// sort is by slug, but conflict records read better in page order.
	var order []string
	kinds := map[string]map[dg.NodeKind][]int{}
	parents := map[string]string{}
	// bestKindConf tracks the confidence of the reading whose kind currently
	// stands. Kept separately because MergedNode.Confidence is the *weakest*
	// reading: comparing against it would let the third page's mediocre kind
	// beat the first page's confident one.
	bestKindConf := map[string]float32{}

	for _, p := range pages {
		idx.perPage[p.Idx] = map[string]bool{}
		illegibleSeen := 0

		for _, n := range p.Extracted.Nodes {
			key := dg.NormaliseName(n.Name)

			// An unreadable box gets a positional identity rather than a
			// slugified guess. Two pages cannot accidentally agree that "box
			// left of the database" is the same box, and they must not: the
			// only honest thing to say is that there is an unread box on page
			// 3 and another on page 4.
			if !n.Legible {
				illegibleSeen++
				slug := fmt.Sprintf("unreadable-p%d-%d", p.Idx+1, illegibleSeen)
				node := MergedNode{
					Slug: slug, Name: strings.TrimSpace(n.Name), Kind: n.Kind,
					Summary: n.Summary, Confidence: n.Confidence,
					Legible: false, Verbatim: n.Verbatim, Pages: []int{p.Idx},
				}
				if node.Name == "" {
					node.Name = fmt.Sprintf("unreadable element %d on page %d", illegibleSeen, p.Idx+1)
				}
				idx.bySlug[slug] = &node
				idx.perPage[p.Idx][slug] = true
				order = append(order, slug)
				continue
			}
			if key == "" {
				continue
			}

			slug, seen := idx.byName[key]
			if !seen {
				slug = dg.Slugify(n.Name)
				if slug == "" {
					continue
				}
				// A legible name that slugifies onto an unreadable placeholder
				// or another entry is disambiguated rather than silently
				// overwriting it.
				slug = uniqueSlug(idx.bySlug, slug)
				idx.byName[key] = slug
				node := MergedNode{
					Slug: slug, Name: strings.TrimSpace(n.Name), Kind: n.Kind,
					Summary: n.Summary, Confidence: n.Confidence, Legible: true,
				}
				idx.bySlug[slug] = &node
				order = append(order, slug)
				kinds[slug] = map[dg.NodeKind][]int{}
				bestKindConf[slug] = n.Confidence
			}

			node := idx.bySlug[slug]
			node.Pages = appendPage(node.Pages, p.Idx)
			idx.perPage[p.Idx][slug] = true

			// The weakest reading governs. A box read clearly on page 1 and
			// barely on page 3 is a box worth a second look; averaging the two
			// hides exactly the page that needs one.
			if n.Confidence < node.Confidence {
				node.Confidence = n.Confidence
			}
			if node.Summary == "" {
				node.Summary = n.Summary
			}
			if kinds[slug] != nil {
				kinds[slug][n.Kind] = appendPage(kinds[slug][n.Kind], p.Idx)
			}
			// The kind from the most confident reading wins, and the
			// disagreement is recorded either way.
			if n.Confidence > bestKindConf[slug] || node.Kind == "" {
				node.Kind = n.Kind
				bestKindConf[slug] = n.Confidence
			}
			if n.Parent != "" && parents[slug] == "" {
				parents[slug] = n.Parent
			}
		}
	}

	// Kind disagreements: recorded, never resolved. Two pages calling the same
	// box a service and a component is a real architectural question, and the
	// person who can answer it is the one reviewing this screen.
	for _, slug := range order {
		byKind := kinds[slug]
		if len(byKind) < 2 {
			continue
		}
		node := idx.bySlug[slug]
		var parts []string
		var touched []int
		for _, k := range dg.NodeKinds() {
			pgs, ok := byKind[k]
			if !ok {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s on %s", k, pageList(pgs)))
			touched = append(touched, pgs...)
		}
		m.Conflicts = append(m.Conflicts, Conflict{
			Kind: "node_kind_disagreement",
			Name: node.Name,
			Detail: fmt.Sprintf("The pages disagree about what %q is: %s. It was kept as %s, from the most confident reading. Nothing was merged away — pick the right one before accepting.",
				node.Name, strings.Join(parts, ", "), node.Kind),
			Pages: dedupPages(touched),
		})
		flagNode(node, fmt.Sprintf("the pages disagree about its kind; kept as %s", node.Kind))
	}

	// Containment, once every name is known. A parent that names nothing on any
	// page is dropped rather than invented, and the drop is surfaced.
	for slug, parentName := range parents {
		node := idx.bySlug[slug]
		parentSlug, ok := idx.byName[dg.NormaliseName(parentName)]
		if !ok || parentSlug == slug {
			flagNode(node, fmt.Sprintf("drawn inside %q, which was not read as a box on any page", parentName))
			continue
		}
		parent := idx.bySlug[parentSlug]
		if !parent.Kind.CanContain() {
			flagNode(node, fmt.Sprintf("drawn inside %q, which was read as a %s; only a boundary or a system may contain",
				parent.Name, parent.Kind))
			continue
		}
		node.Parent = parentSlug
	}

	for _, slug := range order {
		node := idx.bySlug[slug]
		switch {
		case !node.Legible:
			flagNode(node, "the text could not be read")
		case node.Confidence < ReviewThreshold:
			flagNode(node, fmt.Sprintf("read with low confidence (%.2f)", node.Confidence))
		}
		m.Nodes = append(m.Nodes, *node)
	}
	sort.Slice(m.Nodes, func(i, j int) bool { return m.Nodes[i].Slug < m.Nodes[j].Slug })
	return idx
}

// mergeEdges folds every page's arrows in, resolving endpoints across pages.
//
// This is the step that exists because the "continues on the next page" arrow
// is where extraction goes wrong. An arrow whose far end is on another page is
// attached and **flagged**; an arrow whose far end is nowhere is kept as
// unresolved and **not** dropped. A dropped dependency is the one error nobody
// notices.
func mergeEdges(m *Merged, pages []*Page, idx *nodeIndex) {
	type acc struct {
		edge      MergedEdge
		relations map[dg.Relation][]int
		// bestConf is the confidence of the reading whose relation stands, for
		// the same reason nodes track it separately: edge.Confidence is the
		// weakest reading and comparing against it picks the wrong winner.
		bestConf float32
	}
	byKey := map[string]*acc{}
	var order []string

	for _, p := range pages {
		for _, e := range p.Extracted.Edges {
			fromSlug, fromOK := idx.resolve(e.From)
			toSlug, toOK := idx.resolve(e.To)
			if !fromOK && !toOK {
				// Neither end names anything that was read. There is no arrow
				// here to record, only noise.
				continue
			}

			edge := MergedEdge{
				Relation: e.Relation, Label: strings.TrimSpace(e.Label),
				Confidence: e.Confidence, Pages: []int{p.Idx},
			}
			edge.From, edge.To = fromSlug, toSlug

			switch {
			case !fromOK || !toOK:
				// The end that resolved keeps its slug; the end that did not
				// keeps the text the model read, verbatim. That end is not an
				// identity — there is nothing for it to identify — it is
				// evidence of what was written on the paper, and slugifying it
				// would turn "???" into an empty string and throw away the one
				// thing a human needs in order to decide what it was.
				edge.Unresolved = true
				dangling := strings.TrimSpace(e.To)
				if !fromOK {
					dangling = strings.TrimSpace(e.From)
					edge.From = dangling
				} else {
					edge.To = dangling
				}
				reason := fmt.Sprintf("one end names %q, which was not read as a box on any page", dangling)
				if e.ContinuesOffPage {
					reason = fmt.Sprintf("this arrow ran off page %d towards %q, and no page has a box by that name",
						p.Idx+1, dangling)
				}
				flagEdge(&edge, reason)

			case !idx.perPage[p.Idx][toSlug] || !idx.perPage[p.Idx][fromSlug]:
				// Both ends resolved, but not both on the page the arrow was
				// drawn on. That is the merge doing its actual work, and it is
				// where a reviewer should look hardest.
				//
				// Keyed on where the boxes were actually read rather than on
				// the model's continues_off_page flag: an arrow the model
				// marked as leaving the sheet whose target is right there on
				// the same page is a normal arrow and a misread flag, and
				// flagging it would say "joined to a box on another page"
				// about a join that never happened.
				edge.CrossPage = true
				flagEdge(&edge, fmt.Sprintf("read on page %d and joined to a box read on another page", p.Idx+1))
			}

			key := edge.From + "\x00" + edge.To
			existing, ok := byKey[key]
			if !ok {
				a := &acc{
					edge:      edge,
					relations: map[dg.Relation][]int{e.Relation: {p.Idx}},
					bestConf:  e.Confidence,
				}
				byKey[key] = a
				order = append(order, key)
				continue
			}

			existing.relations[e.Relation] = appendPage(existing.relations[e.Relation], p.Idx)
			existing.edge.Pages = appendPage(existing.edge.Pages, p.Idx)
			if e.Confidence > existing.bestConf {
				existing.edge.Relation = e.Relation
				existing.bestConf = e.Confidence
			}
			if e.Confidence < existing.edge.Confidence {
				existing.edge.Confidence = e.Confidence
			}
			if existing.edge.Label == "" {
				existing.edge.Label = strings.TrimSpace(e.Label)
			}
			existing.edge.CrossPage = existing.edge.CrossPage || edge.CrossPage
			existing.edge.Unresolved = existing.edge.Unresolved || edge.Unresolved
			if edge.NeedsReview && !existing.edge.NeedsReview {
				flagEdge(&existing.edge, edge.ReviewReason)
			}
		}
	}

	for _, key := range order {
		a := byKey[key]
		if len(a.relations) > 1 {
			var parts []string
			var touched []int
			for _, r := range dg.Relations() {
				pgs, ok := a.relations[r]
				if !ok {
					continue
				}
				parts = append(parts, fmt.Sprintf("%s on %s", r, pageList(pgs)))
				touched = append(touched, pgs...)
			}
			m.Conflicts = append(m.Conflicts, Conflict{
				Kind: "relation_disagreement",
				Name: a.edge.From + " → " + a.edge.To,
				Detail: fmt.Sprintf("The pages disagree about the arrow from %s to %s: %s. It was kept as %s, from the most confident reading.",
					a.edge.From, a.edge.To, strings.Join(parts, ", "), a.edge.Relation),
				Pages: dedupPages(touched),
			})
			flagEdge(&a.edge, fmt.Sprintf("the pages disagree about the relation; kept as %s", a.edge.Relation))
		}
		if !a.edge.NeedsReview && a.edge.Confidence < ReviewThreshold {
			flagEdge(&a.edge, fmt.Sprintf("read with low confidence (%.2f)", a.edge.Confidence))
		}
		m.Edges = append(m.Edges, a.edge)
	}

	sort.Slice(m.Edges, func(i, j int) bool {
		a, b := m.Edges[i], m.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Relation < b.Relation
	})
}

// resolve maps a name as written on a page onto a merged slug.
func (idx *nodeIndex) resolve(name string) (string, bool) {
	slug, ok := idx.byName[dg.NormaliseName(name)]
	return slug, ok
}

// ── helpers ──────────────────────────────────────────────────────────────────

func flagNode(n *MergedNode, reason string) {
	n.NeedsReview = true
	if n.ReviewReason == "" {
		n.ReviewReason = reason
		return
	}
	if !strings.Contains(n.ReviewReason, reason) {
		n.ReviewReason += "; " + reason
	}
}

func flagEdge(e *MergedEdge, reason string) {
	e.NeedsReview = true
	if e.ReviewReason == "" {
		e.ReviewReason = reason
		return
	}
	if !strings.Contains(e.ReviewReason, reason) {
		e.ReviewReason += "; " + reason
	}
}

func uniqueSlug(taken map[string]*MergedNode, slug string) string {
	if _, clash := taken[slug]; !clash {
		return slug
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", slug, n)
		if _, clash := taken[candidate]; !clash {
			return candidate
		}
	}
}

func appendPage(pages []int, idx int) []int {
	for _, p := range pages {
		if p == idx {
			return pages
		}
	}
	return append(pages, idx)
}

func dedupPages(pages []int) []int {
	sort.Ints(pages)
	out := pages[:0]
	for i, p := range pages {
		if i == 0 || pages[i-1] != p {
			out = append(out, p)
		}
	}
	return out
}

// pageList renders page indices as humans count them, from one.
func pageList(pages []int) string {
	pages = dedupPages(append([]int(nil), pages...))
	parts := make([]string, 0, len(pages))
	for _, p := range pages {
		parts = append(parts, fmt.Sprintf("%d", p+1))
	}
	if len(parts) == 1 {
		return "page " + parts[0]
	}
	return "pages " + strings.Join(parts, " and ")
}

// mergeHeader picks a title and a level from what the pages claimed.
func mergeHeader(pages []*Page) (string, dg.Level) {
	var title string
	votes := map[dg.Level]int{}
	for _, p := range pages {
		if title == "" {
			title = strings.TrimSpace(p.Extracted.Title)
		}
		if p.Extracted.Level.Valid() {
			votes[p.Extracted.Level]++
		}
	}

	best, bestVotes := dg.Level(""), 0
	for _, l := range dg.Levels() {
		if votes[l] > bestVotes {
			best, bestVotes = l, votes[l]
		}
	}
	return title, best
}
