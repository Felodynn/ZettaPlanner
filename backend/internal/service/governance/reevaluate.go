package governance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
)

// Re-evaluate: ask the model to look again at a diagram a human has corrected.
//
// **It returns a Proposal, never a write** (§5.4) — including when the person
// who triggered it is a manager who could have written it directly. A stray
// click must not rewrite a plan somebody corrected by hand, and the whole value
// of the flow depends on the human corrections surviving it.
//
// The highest-signal input is the provenance. `human_edited` on a box says a
// person looked at what the model read off the page and decided otherwise —
// which is worth more than anything else the vision model will be shown, and is
// why §3.7 pays for the field at all. So the prompt names, element by element,
// which ones a human touched and tells the model those are corrections rather
// than candidates.

// MaxReevaluatePages bounds how much of a document is re-read in one request.
//
// Re-evaluate is synchronous: §7.1 says the endpoint returns a Proposal, and
// returning a job id would mean a second state machine, a second progress
// stream and a second thing that can be left half-finished. The cost of that
// choice is a slow request on a multi-page ingest — four pages at roughly eight
// seconds each — so it is bounded rather than unbounded, and the proposal's
// rationale says how many pages were actually read. A diagram with no ingest
// behind it, which is most of them, is one text call.
const MaxReevaluatePages = 4

// ReevaluateResult is what one re-evaluation produced.
type ReevaluateResult struct {
	Proposal *pr.Proposal
	// PagesRead is how many source pages were shown to the model. Zero means
	// the diagram has no ingest behind it and was re-read from its
	// serialization alone.
	PagesRead int
	// Model is what answered, recorded because two runs on different models are
	// not comparable.
	Model string
	// Skipped names what was deliberately left out, so "we re-read it" never
	// silently means "we re-read some of it".
	Skipped string
}

// Reevaluate re-examines a diagram and raises a proposal.
func (s *Service) Reevaluate(ctx context.Context, diagramID uuid.UUID, p policy.Principal) (*ReevaluateResult, error) {
	client, err := s.visionModel()
	if err != nil {
		return nil, err
	}

	d, err := s.diagramRep.ByID(ctx, s.db.Pool, diagramID)
	if err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, s.db.Pool, p, d.ProjectID); err != nil {
		return nil, err
	}
	graph, err := s.diagramRep.LoadGraph(ctx, s.db.Pool, diagramID)
	if err != nil {
		return nil, err
	}
	if len(graph.Nodes) == 0 {
		return nil, fmt.Errorf("%w: %s has no nodes yet, so there is nothing to re-examine. "+
			"Ingest a drawing or draw one first", domain.ErrInvalid, d.Slug)
	}

	pages, skipped, err := s.sourcePages(ctx, diagramID)
	if err != nil {
		return nil, err
	}

	state := describeGraph(d, graph)
	corrections, err := s.readCorrections(ctx, client, state, pages)
	if err != nil {
		return nil, err
	}

	changes, notes := correctionsToChanges(graph, d, corrections)
	if len(changes) == 0 {
		// Not a failure and not silence: the model looked and found nothing to
		// change, which is a real answer and a common one for a diagram
		// somebody has already corrected by hand.
		return &ReevaluateResult{PagesRead: len(pages), Model: client.Config().Name, Skipped: skipped}, nil
	}

	prop := &pr.Proposal{
		ID:        uuid.Must(uuid.NewV7()),
		ProjectID: d.ProjectID,
		ActorID:   p.WritingActorID(),
		Title:     fmt.Sprintf("Re-evaluation of %s", d.Name),
		Rationale: reevaluateRationale(client.Config().Name, len(pages), skipped, notes),
		Target:    pr.TargetDiagram,
		Status:    pr.StatusPending,
		CreatedAt: time.Now(),
		Changes:   changes,
	}
	if p.IsAgent() {
		prop.OnBehalfOf = p.OnBehalfOf()
	}
	if err := pr.Validate(prop); err != nil {
		return nil, err
	}

	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.proposals.Create(ctx, tx, prop); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &d.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionProposalCreated, SubjectID: &prop.ID,
			After: map[string]any{
				"title": prop.Title, "target_kind": string(prop.Target),
				"changes": len(prop.Changes), "source": "reevaluate",
				"diagram_id": d.ID.String(), "pages_read": len(pages), "model": client.Config().Name,
			},
		})
	})
	if err != nil {
		return nil, err
	}

	s.publish(d.ProjectID, "proposal.created", map[string]any{
		"proposal_id": prop.ID, "title": prop.Title, "target_kind": string(prop.Target),
		"changes": len(prop.Changes), "diagram_id": d.ID, "source": "reevaluate",
	})

	stored, err := s.proposals.ByID(ctx, s.db.Pool, prop.ID)
	if err != nil {
		return nil, err
	}
	return &ReevaluateResult{
		Proposal: stored, PagesRead: len(pages), Model: client.Config().Name, Skipped: skipped,
	}, nil
}

func (s *Service) visionModel() (*llm.Client, error) {
	if s.registry == nil {
		return nil, fmt.Errorf("%w: no model registry is configured, so nothing can re-examine a diagram",
			llm.ErrModelUnavailable)
	}
	candidates, err := s.registry.Candidates("vision")
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: no model is configured for the vision role; add one to models.yaml under "+
			"roles.vision.prefer", llm.ErrModelUnavailable)
	}
	return candidates[0], nil
}

// sourcePages finds the drawings this diagram was built from.
func (s *Service) sourcePages(ctx context.Context, diagramID uuid.UUID) ([]string, string, error) {
	ingests, err := s.ingests.ForDiagram(ctx, s.db.Pool, diagramID, 5)
	if err != nil {
		return nil, "", err
	}

	var paths []string
	total := 0
	for _, in := range ingests {
		pages, err := s.ingests.Pages(ctx, s.db.Pool, in.ID)
		if err != nil {
			return nil, "", err
		}
		for _, page := range pages {
			if page.ImagePath == "" {
				continue
			}
			// A page whose image has been cleaned off disk is skipped rather
			// than failing the run: the graph is still worth re-reading, and
			// the rationale will say how many pages were actually seen.
			if _, err := os.Stat(page.ImagePath); err != nil {
				continue
			}
			total++
			if len(paths) < MaxReevaluatePages {
				paths = append(paths, page.ImagePath)
			}
		}
	}

	var skipped string
	if total > len(paths) {
		skipped = fmt.Sprintf("%d of %d source pages were not re-read (the limit is %d per run)",
			total-len(paths), total, MaxReevaluatePages)
	}
	return paths, skipped, nil
}

// readCorrections runs the model once per page, or once over the serialization
// when there are no pages.
//
// **One call per page, never one call for the document.** Q3 measured this on
// the same schema, model and pages: per-page produced 7 nodes and 7 edges,
// whole-document produced 0 nodes on all three trials because the model stops
// emitting the first array once the input is multi-image. The same rule that
// governs ingest governs re-reading.
func (s *Service) readCorrections(ctx context.Context, client *llm.Client, state string, pages []string) ([]correction, error) {
	if len(pages) == 0 {
		out, err := s.askOnce(ctx, client, state, "")
		if err != nil {
			return nil, err
		}
		return out, nil
	}

	var all []correction
	for i, path := range pages {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading page image: %w", err)
		}
		out, err := s.askOnce(ctx, client, state,
			base64.StdEncoding.EncodeToString(raw))
		if err != nil {
			return nil, fmt.Errorf("re-reading page %d: %w", i+1, err)
		}
		all = append(all, out...)
	}
	return all, nil
}

// maxReevaluateAttempts is how many times one page is offered to the model.
//
// Two, for the same reason ingest gives two (D38): Ollama Cloud accepts the
// `format` field and ignores it, so a cloud model answers a schema-constrained
// request in prose. Measured here on `gemma4:31b-cloud`, which replied "The
// `token-store` is incorrectly typed as a `queue`; it should be a `datastore`."
// — a correct observation in a shape nothing can apply. The prompt restates the
// object it wants, and the second attempt carries the teaching error, which is
// the one instruction that reliably fixes it.
const maxReevaluateAttempts = 2

func (s *Service) askOnce(ctx context.Context, client *llm.Client, state, image string) ([]correction, error) {
	user := llm.Message{Role: "user", Content: "Re-examine this diagram and return only what is still wrong, " +
		"as a single JSON object."}
	if image != "" {
		user.Content = "Here is the page this diagram was read from. Compare it against the current graph above " +
			"and return only what is still wrong, as a single JSON object."
		user.Images = []string{image}
	}

	think := false
	temperature := 0.0
	messages := []llm.Message{
		{Role: "system", Content: reevaluatePrompt + "\n\n" + state},
		user,
	}

	var lastErr error
	for attempt := 1; attempt <= maxReevaluateAttempts; attempt++ {
		resp, err := client.Chat(ctx, llm.ChatRequest{
			Messages: messages,
			Format:   reevaluateSchema(),
			Think:    &think,
			Options:  llm.Options{Temperature: &temperature},
		})
		if err != nil {
			// A transport failure is not something an identical second call
			// fixes, and retrying one silently hides a rate limit behind a wait.
			return nil, err
		}
		out, err := parseCorrections(resp.Message.Content)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if attempt == maxReevaluateAttempts {
			break
		}
		messages = append(messages,
			llm.Message{Role: "assistant", Content: resp.Message.Content},
			llm.Message{Role: "user", Content: err.Error() + "\n\n" + reevaluateShape +
				"\n\nAnswer again, as that object and nothing else."})
	}
	return nil, lastErr
}

// ── what the model is shown ──────────────────────────────────────────────────

const reevaluatePrompt = `You are re-examining an architecture diagram that has already been extracted once and then corrected by hand.

The provenance of every element is given below. It is the most important thing you will read here:

  human_created / human_edited — a person decided this. **Treat it as correct.** Do not propose changing
    it back to what an extraction produced. If the page appears to disagree with it, the person had a
    reason you cannot see, and a note is the right answer rather than a change.
  vlm_extracted — read off the page by a model and never checked. This is where mistakes live.
  agent_proposed / agent_committed — written by the planner agent.

Return only what is still wrong. An empty list is a good answer and a common one.

Node kinds: system, service, component, datastore, queue, external_system, actor, api, job, boundary, note.
Edge relations: calls, depends_on, reads_from, writes_to, publishes_to, subscribes_to, contains, deploys_to, data_flow.
Use data_flow when nothing more specific is true rather than inventing a relation.

Never invent a label for something you cannot read. Say so in the reason instead.

` + reevaluateShape

// reevaluateShape restates the schema in the prompt.
//
// Belt and braces on purpose: the `format` field carries the same schema, and a
// cloud model discards it (D38). Prose describing the observation instead of the
// object is the failure mode, and stating the object in the instructions is what
// makes the first attempt land often enough to matter.
const reevaluateShape = `Answer with a single JSON object and nothing else — no prose, no explanation, no fence:

{"corrections": [
  {"action": "add_node",    "slug": "...", "kind": "...", "name": "...", "summary": "...", "reason": "..."},
  {"action": "update_node", "slug": "...", "kind": "...", "name": "...", "summary": "...", "reason": "..."},
  {"action": "remove_node", "slug": "...", "reason": "..."},
  {"action": "add_edge",    "from": "...", "to": "...", "relation": "...", "label": "...", "reason": "..."},
  {"action": "remove_edge", "from": "...", "to": "...", "relation": "...", "reason": "..."}
]}

` + "`slug`" + ` is the identifier in the NODES list below, not the display name. ` + "`from`" + ` and ` + "`to`" + ` are slugs too.
If nothing is wrong, answer {"corrections": []}.`

// describeGraph renders the current state with per-element provenance.
func describeGraph(d *dg.Diagram, g *dg.Graph) string {
	var b strings.Builder
	fmt.Fprintf(&b, "CURRENT DIAGRAM: %s (%s), level %s\n\nNODES\n", d.Name, d.Slug, d.Level)

	nodes := append([]*dg.Node(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Slug() < nodes[j].Slug() })
	for _, n := range nodes {
		fmt.Fprintf(&b, "  %s  kind=%s  name=%q  provenance=%s",
			n.Slug(), n.Kind(), n.Label(), n.EffectiveProvenance())
		if n.ParentSlug != "" {
			fmt.Fprintf(&b, "  inside=%s", n.ParentSlug)
		}
		if n.Entity != nil && n.Entity.Summary != "" {
			fmt.Fprintf(&b, "\n      %s", n.Entity.Summary)
		}
		b.WriteString("\n")
	}

	b.WriteString("\nEDGES\n")
	type line struct{ text, prov string }
	var edges []line
	for _, e := range g.Edges {
		from, to := g.NodeByID(e.FromNodeID), g.NodeByID(e.ToNodeID)
		if from == nil || to == nil {
			continue
		}
		text := fmt.Sprintf("%s %s %s", from.Slug(), e.Relation, to.Slug())
		if e.Label != "" {
			text += fmt.Sprintf(" (%q)", e.Label)
		}
		edges = append(edges, line{text, string(e.Provenance)})
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].text < edges[j].text })
	for _, e := range edges {
		fmt.Fprintf(&b, "  %s  provenance=%s\n", e.text, e.prov)
	}
	if len(edges) == 0 {
		b.WriteString("  (none)\n")
	}

	// Stated rather than left implicit. A count is something the model can act
	// on; scanning twenty provenance strings for the two that differ is not.
	edited := 0
	for _, n := range g.Nodes {
		if humanDecided(n.EffectiveProvenance()) {
			edited++
		}
	}
	fmt.Fprintf(&b, "\n%d of %d nodes carry human provenance. Those are decisions, not candidates.\n",
		edited, len(g.Nodes))
	return b.String()
}

func reevaluateSchema() json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "properties":{
	    "corrections":{"type":"array","items":{"type":"object","properties":{
	      "action":{"type":"string","enum":["add_node","update_node","remove_node","add_edge","remove_edge"]},
	      "slug":{"type":"string","description":"The node this concerns, for the node actions."},
	      "kind":{"type":"string"},
	      "name":{"type":"string"},
	      "summary":{"type":"string"},
	      "from":{"type":"string"},
	      "to":{"type":"string"},
	      "relation":{"type":"string"},
	      "label":{"type":"string"},
	      "reason":{"type":"string","description":"One sentence a human will read before deciding."}},
	      "required":["action","reason"]}}},
	  "required":["corrections"]}`)
}

type correction struct {
	Action   string `json:"action"`
	Slug     string `json:"slug"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Summary  string `json:"summary"`
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
	Label    string `json:"label"`
	Reason   string `json:"reason"`
}

func parseCorrections(content string) ([]correction, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: the model returned no content at all", domain.ErrConflict)
	}
	// Ollama Cloud discards `format` (D38), so the object arrives inside prose.
	// Finding it is normalising a wrapper, not parsing around the failure mode:
	// everything below still rejects an answer that does not match.
	body, ok := firstJSONObject(trimmed)
	if !ok {
		return nil, fmt.Errorf("%w: the model's answer contains no JSON object", domain.ErrConflict)
	}
	var out struct {
		Corrections []correction `json:"corrections"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, fmt.Errorf("%w: the model's answer is not valid JSON (%v)", domain.ErrConflict, err)
	}
	return out.Corrections, nil
}

// firstJSONObject extracts the first balanced {...}, ignoring braces in strings.
func firstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

// ── turning an answer into changes ───────────────────────────────────────────

// correctionsToChanges is deterministic code, not a second model call.
//
// The same reasoning as the ingest merge (D37): the judgements involved — is
// this the box we already have, does this arrow already exist — are exactly the
// ones a confident wrong answer ruins, and being pure code makes them testable
// with no database and no model. It also enforces the one rule the prompt can
// only request: **a correction that would overwrite human provenance is dropped
// and reported**, because a request is not an enforcement mechanism.
func correctionsToChanges(g *dg.Graph, d *dg.Diagram, in []correction) ([]pr.Change, []string) {
	var (
		changes []pr.Change
		notes   []string
		seen    = map[string]bool{}
	)
	id := d.ID

	for _, c := range in {
		key := strings.ToLower(strings.Join([]string{c.Action, c.Slug, c.From, c.To, c.Relation}, "|"))
		if seen[key] {
			// The same correction from two pages is one correction. Pages
			// overlap, and a proposal listing it twice reads as though the model
			// felt strongly rather than as though it saw the box twice.
			continue
		}
		seen[key] = true

		switch c.Action {
		case "add_node", "update_node", "remove_node":
			slug := strings.TrimSpace(c.Slug)
			if slug == "" {
				slug = dg.Slugify(c.Name)
			}
			if slug == "" {
				continue
			}
			node := diagramsvc.FindNode(g, slug)

			if c.Action == "add_node" {
				if node != nil {
					continue
				}
				if c.Kind != "" && dg.ValidateNodeKind(dg.NodeKind(c.Kind)) != nil {
					notes = append(notes, fmt.Sprintf("dropped a new node %q: %q is not a node kind", slug, c.Kind))
					continue
				}
				changes = append(changes, pr.Change{
					Op: pr.OpCreate, Target: pr.ChangeNode, TargetID: &id,
					After: withDiagram(d, map[string]any{
						"slug": slug, "kind": c.Kind, "name": orSlug(c.Name, slug),
						"summary": c.Summary, "reason": c.Reason,
					}),
				})
				continue
			}
			if node == nil {
				continue
			}
			if humanDecided(node.EffectiveProvenance()) {
				notes = append(notes, fmt.Sprintf("dropped a change to %q: a human set it, and the prompt says "+
					"those are decisions rather than candidates (%s)", slug, oneLine(c.Reason)))
				continue
			}
			if c.Action == "remove_node" {
				changes = append(changes, pr.Change{
					Op: pr.OpDelete, Target: pr.ChangeNode, TargetID: &id,
					Before: diagramsvc.NodeSnapshot(node),
					After:  withDiagram(d, map[string]any{"slug": slug, "reason": c.Reason}),
				})
				continue
			}
			if c.Kind != "" && dg.ValidateNodeKind(dg.NodeKind(c.Kind)) != nil {
				notes = append(notes, fmt.Sprintf("dropped a change to %q: %q is not a node kind", slug, c.Kind))
				continue
			}
			after := withDiagram(d, map[string]any{"slug": slug, "reason": c.Reason})
			addIf(after, "kind", c.Kind)
			addIf(after, "name", c.Name)
			addIf(after, "summary", c.Summary)
			if len(after) <= 3 {
				continue
			}
			changes = append(changes, pr.Change{
				Op: pr.OpUpdate, Target: pr.ChangeNode, TargetID: &id,
				Before: diagramsvc.NodeSnapshot(node), After: after,
			})

		case "add_edge", "remove_edge":
			from, to := strings.TrimSpace(c.From), strings.TrimSpace(c.To)
			rel := dg.Relation(strings.TrimSpace(c.Relation))
			if from == "" || to == "" || rel == "" {
				continue
			}
			if dg.ValidateRelation(rel) != nil {
				notes = append(notes, fmt.Sprintf("dropped an arrow %s → %s: %q is not a relation", from, to, rel))
				continue
			}
			edge := diagramsvc.FindEdge(g, from, to, rel)

			if c.Action == "add_edge" {
				if edge != nil {
					continue
				}
				if diagramsvc.FindNode(g, from) == nil || diagramsvc.FindNode(g, to) == nil {
					notes = append(notes, fmt.Sprintf("dropped an arrow %s %s %s: one end is not a box in this "+
						"diagram", from, rel, to))
					continue
				}
				changes = append(changes, pr.Change{
					Op: pr.OpCreate, Target: pr.ChangeEdge, TargetID: &id,
					After: withDiagram(d, map[string]any{
						"from": from, "to": to, "relation": string(rel),
						"label": c.Label, "reason": c.Reason,
					}),
				})
				continue
			}
			if edge == nil {
				continue
			}
			if humanDecided(edge.Provenance) {
				notes = append(notes, fmt.Sprintf("dropped a removal of %s %s %s: a human drew it", from, rel, to))
				continue
			}
			changes = append(changes, pr.Change{
				Op: pr.OpDelete, Target: pr.ChangeEdge, TargetID: &id,
				Before: diagramsvc.EdgeSnapshot(g, edge),
				After: withDiagram(d, map[string]any{
					"from": from, "to": to, "relation": string(rel), "reason": c.Reason,
				}),
			})
		}
	}
	return changes, notes
}

// humanDecided is the rule the whole flow turns on: what a person set is not
// re-decided by a model that already read this page once and got it wrong.
func humanDecided(p dg.Provenance) bool {
	return p == dg.ProvHumanCreated || p == dg.ProvHumanEdited
}

func withDiagram(d *dg.Diagram, m map[string]any) map[string]any {
	m["diagram"] = d.Slug
	return m
}

func addIf(m map[string]any, key, value string) {
	if strings.TrimSpace(value) != "" {
		m[key] = value
	}
}

func orSlug(name, slug string) string {
	if strings.TrimSpace(name) == "" {
		return slug
	}
	return name
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:157] + "…"
	}
	return s
}

// reevaluateRationale says what was read and what was refused.
//
// The dropped corrections go in the rationale rather than being discarded: "we
// re-read it" must never quietly mean "we re-read it and threw some of the
// answer away", and a reviewer who sees that the model wanted to undo their
// correction learns something about the model.
func reevaluateRationale(model string, pages int, skipped string, notes []string) string {
	var b strings.Builder
	b.WriteString("Raised by re-evaluation (§5.4), not by a person. Nothing here has been applied.\n\n")

	switch pages {
	case 0:
		b.WriteString("This diagram has no source pages on disk, so it was re-read from its current graph " +
			"alone — the model had no drawing to compare against, which is a weaker question than the flow " +
			"is designed for.")
	case 1:
		b.WriteString("The source page was re-read and compared against the current graph.")
	default:
		fmt.Fprintf(&b, "%d source pages were re-read, one model call each, and compared against the current graph.", pages)
	}
	fmt.Fprintf(&b, " Model: %s.", model)
	if skipped != "" {
		fmt.Fprintf(&b, " %s.", skipped)
	}

	b.WriteString("\n\nElements a human created or edited were shown to the model as decisions rather than " +
		"candidates, and any correction that would have overwritten one was dropped here rather than offered.")
	if len(notes) > 0 {
		fmt.Fprintf(&b, "\n\nDropped (%d):\n", len(notes))
		for _, n := range notes {
			b.WriteString("  - " + n + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
