package contextsvc

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/contextbuild"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/vector"
)

// Request is what to assemble context for.
type Request struct {
	// Message is the turn's prompt. Expansion starts from what it names.
	Message string
	// ItemRef pulls one work item in explicitly, whether or not the message
	// mentions it — "tell me about ZP-14" without saying ZP-14.
	ItemRef string
	// Model names which model's assembly_budget to target. Empty uses the
	// planner role's first choice.
	Model string
	// MaxSpill caps retrieval before the budget gets a say.
	MaxSpill int
}

// Result is an assembled context plus everything that went into it, so that
// `context show` can print the parts as well as the whole.
type Result struct {
	Input   contextbuild.Input
	Context *contextbuild.Context
	Text    string

	// References is what the message resolved to. Printed by `context show`,
	// because "the context is about the wrong item" is a failure that looks
	// exactly like "the model is bad at this".
	References contextbuild.References
}

// Assemble builds the three layers for one turn.
//
// Everything is read in **one transaction**. Layers gathered across several
// snapshots can disagree — a skeleton listing an item as open beside an
// expansion showing it done — and a model handed a contradiction resolves it
// silently and confidently.
func (s *Service) Assemble(ctx context.Context, projectID uuid.UUID, req Request, p policy.Principal) (*Result, error) {
	var res *Result
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.requireMember(ctx, tx, p, projectID); err != nil {
			return err
		}
		var err error
		res, err = s.assemble(ctx, tx, projectID, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *Service) assemble(ctx context.Context, q store.Querier, projectID uuid.UUID, req Request) (*Result, error) {
	project, err := s.projects.ByID(ctx, q, projectID)
	if err != nil {
		return nil, err
	}

	skeleton, err := s.skeleton(ctx, q, project)
	if err != nil {
		return nil, err
	}

	entities, err := s.entities.ListByProject(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	named := make([]contextbuild.NamedEntity, 0, len(entities))
	for _, e := range entities {
		named = append(named, contextbuild.NamedEntity{Slug: e.Slug, Name: e.Name})
	}
	refs := contextbuild.FindReferences(req.Message, project.Key, named)
	if req.ItemRef != "" {
		refs.ItemRefs = appendUnique(refs.ItemRefs, normaliseRef(project.Key, req.ItemRef))
	}

	expansion, err := s.expand(ctx, q, projectID, refs)
	if err != nil {
		return nil, err
	}

	spill, err := s.spill(ctx, q, projectID, req)
	if err != nil {
		return nil, err
	}

	in := contextbuild.Input{
		Message:   req.Message,
		Skeleton:  skeleton,
		Expansion: expansion,
		Spill:     spill,
		Budget:    s.budget(req.Model),
		Now:       time.Now(),
	}
	built := contextbuild.Assemble(in)
	return &Result{Input: in, Context: built, Text: built.Render(in), References: refs}, nil
}

// budget resolves the token ceiling from the target model.
//
// `assembly_budget`, not `num_ctx` (§8). glm-5.2:cloud advertises a
// million-token window; the model that has to find one fact in 900,000 tokens
// of plan is not the model anyone wants answering.
func (s *Service) budget(modelName string) contextbuild.Budget {
	if s.registry == nil {
		return contextbuild.Budget{Tokens: contextbuild.DefaultBudget, Model: "(no registry configured)"}
	}
	if modelName != "" {
		if c, ok := s.registry.Client(modelName); ok {
			return contextbuild.Budget{Tokens: c.Config().AssemblyBudget, Model: c.Config().Model}
		}
	}
	if candidates, err := s.registry.Candidates("planner"); err == nil && len(candidates) > 0 {
		c := candidates[0]
		return contextbuild.Budget{Tokens: c.Config().AssemblyBudget, Model: c.Config().Model}
	}
	return contextbuild.Budget{Tokens: contextbuild.DefaultBudget, Model: "(no planner model configured)"}
}

// ── layer 1 ──────────────────────────────────────────────────────────────────

func (s *Service) skeleton(ctx context.Context, q store.Querier, project *domain.Project) (contextbuild.Skeleton, error) {
	sk := contextbuild.Skeleton{
		Project: contextbuild.ProjectMeta{
			ID: project.ID, Key: project.Key, Name: project.Name,
			Description: project.Description, Status: project.Status,
		},
	}

	milestones, err := s.milestones.ListByProject(ctx, q, project.ID)
	if err != nil {
		return sk, err
	}
	counts, err := s.milestoneCounts(ctx, q, project.ID)
	if err != nil {
		return sk, err
	}
	for _, m := range milestones {
		if !m.Status.Open() {
			continue
		}
		sk.Milestones = append(sk.Milestones, contextbuild.MilestoneView{
			Milestone: m, Total: counts[m.ID][0], Open: counts[m.ID][1],
		})
	}

	items, err := s.items.List(ctx, q, project.ID, repo.WorkItemFilter{})
	if err != nil {
		return sk, err
	}
	milestoneNames := map[uuid.UUID]string{}
	for _, m := range milestones {
		milestoneNames[m.ID] = m.Name
	}
	var assignees []uuid.UUID
	open := make([]*pl.WorkItem, 0, len(items))
	for _, w := range items {
		if !w.Status.Open() {
			continue
		}
		open = append(open, w)
		if w.AssigneeID != nil {
			assignees = append(assignees, *w.AssigneeID)
		}
	}
	names, err := s.users.DisplayNames(ctx, q, assignees)
	if err != nil {
		return sk, err
	}
	for _, w := range open {
		v := contextbuild.ItemView{Item: w}
		if w.AssigneeID != nil {
			v.AssigneeName = names[*w.AssigneeID]
		}
		if w.MilestoneID != nil {
			v.MilestoneName = milestoneNames[*w.MilestoneID]
		}
		sk.OpenItems = append(sk.OpenItems, v)
	}

	if sk.Deviations, err = s.deviations.ListByProject(ctx, q, project.ID, repo.DeviationFilter{OpenOnly: true}); err != nil {
		return sk, err
	}
	if sk.Notes, err = s.notes.ListByProject(ctx, q, project.ID, dg.NoteOpen); err != nil {
		return sk, err
	}

	approved, err := s.proposals.Decided(ctx, q, project.ID, "approved", 8)
	if err != nil {
		return sk, err
	}
	sk.Decisions = toDecisions(approved)

	// Rejections are fetched with a larger limit than approvals and are never
	// dropped by the budget (§5.2). Without them the agent re-proposes what was
	// already refused, and the human concludes the tool does not listen.
	rejected, err := s.proposals.Decided(ctx, q, project.ID, "rejected", 12)
	if err != nil {
		return sk, err
	}
	sk.Rejections = toDecisions(rejected)
	return sk, nil
}

func (s *Service) milestoneCounts(ctx context.Context, q store.Querier, projectID uuid.UUID) (map[uuid.UUID][2]int, error) {
	const sql = `
		SELECT milestone_id, count(*), count(*) FILTER (WHERE status <> ALL($2::text[]))
		  FROM work_items
		 WHERE project_id = $1 AND deleted_at IS NULL AND milestone_id IS NOT NULL
		 GROUP BY milestone_id`
	rows, err := q.Query(ctx, sql, projectID, pl.ClosedStatuses())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID][2]int{}
	for rows.Next() {
		var (
			id          uuid.UUID
			total, open int
		)
		if err := rows.Scan(&id, &total, &open); err != nil {
			return nil, err
		}
		out[id] = [2]int{total, open}
	}
	return out, rows.Err()
}

// ── layer 2 ──────────────────────────────────────────────────────────────────

// expand answers by traversal. **No vector search is involved and none may be.**
//
// "What blocks ZP-14" has a right answer that a query returns and an embedding
// approximates, and the approximation is worse than nothing because it reads as
// authoritative. This is the part a vector store cannot do (§6.3 layer 2).
func (s *Service) expand(ctx context.Context, q store.Querier, projectID uuid.UUID, refs contextbuild.References) (contextbuild.Expansion, error) {
	var out contextbuild.Expansion
	if refs.Empty() {
		return out, nil
	}

	entityIDs := map[uuid.UUID]bool{}

	for _, ref := range refs.ItemRefs {
		item, err := s.items.ByRef(ctx, q, projectID, ref)
		if err != nil {
			if isNotFound(err) {
				continue // named something that does not exist; say nothing rather than guess
			}
			return out, err
		}
		ex, err := s.expandItem(ctx, q, item, "named in the request")
		if err != nil {
			return out, err
		}
		for _, r := range ex.Refs {
			entityIDs[r.EntityID] = true
		}
		out.Items = append(out.Items, ex)
	}

	bySlug, err := s.entities.BySlugs(ctx, q, projectID, refs.EntitySlugs)
	if err != nil {
		return out, err
	}
	for _, e := range bySlug {
		entityIDs[e.ID] = true
	}

	if len(entityIDs) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(entityIDs))
	for id := range entityIDs {
		ids = append(ids, id)
	}
	loaded, err := s.entities.ByIDs(ctx, q, ids)
	if err != nil {
		return out, err
	}
	hops, err := s.graphs.Hops(ctx, q, projectID, ids)
	if err != nil {
		return out, err
	}
	byEntity := map[uuid.UUID][]contextbuild.Hop{}
	for _, h := range hops {
		byEntity[h.EntityID] = append(byEntity[h.EntityID], contextbuild.Hop{
			Relation: h.Relation, Label: h.Label, OtherSlug: h.OtherSlug,
			OtherName: h.OtherName, OtherKind: h.OtherKind,
			DiagramSlug: h.DiagramSlug, Inbound: h.Inbound,
		})
	}
	for _, id := range sortedIDs(ids, loaded) {
		out.Entities = append(out.Entities, contextbuild.EntitySlice{
			Entity: loaded[id], Hops: byEntity[id],
		})
	}
	return out, nil
}

func (s *Service) expandItem(ctx context.Context, q store.Querier, item *pl.WorkItem, reason string) (contextbuild.ExpandedItem, error) {
	ex := contextbuild.ExpandedItem{
		ItemView: contextbuild.ItemView{Item: item},
		Reason:   reason,
	}
	if item.AssigneeID != nil {
		names, err := s.users.DisplayNames(ctx, q, []uuid.UUID{*item.AssigneeID})
		if err != nil {
			return ex, err
		}
		ex.AssigneeName = names[*item.AssigneeID]
	}
	if item.MilestoneID != nil {
		m, err := s.milestones.ByID(ctx, q, *item.MilestoneID)
		if err != nil && !isNotFound(err) {
			return ex, err
		}
		if m != nil {
			ex.MilestoneName = m.Name
		}
	}

	outgoing, err := s.deps.Outgoing(ctx, q, item.ID)
	if err != nil {
		return ex, err
	}
	incoming, err := s.deps.Incoming(ctx, q, item.ID)
	if err != nil {
		return ex, err
	}
	for _, d := range outgoing {
		if d.Kind == pl.DepBlocks {
			ex.Blocks = append(ex.Blocks, d)
		} else {
			ex.Relates = append(ex.Relates, d)
		}
	}
	for _, d := range incoming {
		if d.Kind == pl.DepBlocks {
			ex.BlockedBy = append(ex.BlockedBy, d)
		} else {
			ex.Relates = append(ex.Relates, d)
		}
	}
	if ex.SubTasks, err = s.subtasks.ListByItem(ctx, q, item.ID); err != nil {
		return ex, err
	}
	if ex.Checklist, err = s.checklists.ListByItem(ctx, q, item.ID); err != nil {
		return ex, err
	}
	if ex.Refs, err = s.refs.ListByItem(ctx, q, item.ID); err != nil {
		return ex, err
	}
	return ex, nil
}

// ── layer 3 ──────────────────────────────────────────────────────────────────

// spill searches unstructured text only.
//
// Every reason for finding nothing is stated rather than left as an empty
// section: no embedding model, nothing indexed, a corpus in a different
// embedding space. An empty section reads as "there is nothing relevant", and
// three of those four situations mean "nothing was looked at".
func (s *Service) spill(ctx context.Context, q store.Querier, projectID uuid.UUID, req Request) (contextbuild.Spill, error) {
	var out contextbuild.Spill
	if len(req.Message) == 0 {
		out.Unavailable = "no message was given to search with"
		return out, nil
	}

	client, why := s.embedder()
	if client == nil {
		out.Unavailable = why
		return out, nil
	}

	chunks := repo.NewChunks(q)
	indexed, err := chunks.CountByProject(ctx, projectID)
	if err != nil {
		return out, err
	}
	if indexed == 0 {
		out.Unavailable = "nothing in this project has been indexed yet — spill is written when a worklog is"
		return out, nil
	}

	res, err := client.Embed(ctx, req.Message)
	if err != nil {
		// A retrieval failure is not a context failure. The other two layers
		// are complete and useful, and refusing the whole assembly because a
		// similarity search timed out would take a working planner offline for
		// the sake of its least authoritative layer.
		out.Unavailable = "the embedding model did not answer: " + err.Error()
		return out, nil
	}

	limit := req.MaxSpill
	if limit <= 0 {
		limit = 12
	}
	hits, err := chunks.Search(ctx, vector.Query{
		ProjectID: projectID,
		Embedding: res.Embeddings[0],
		Model:     client.Config().Model,
		Kinds:     []vector.SourceKind{vector.SourceWorklog, vector.SourceMessage, vector.SourceDocument},
		Limit:     limit,
		MinScore:  0.25,
	})
	if err != nil {
		var mismatch *vector.ErrModelMismatch
		if errors.As(err, &mismatch) {
			// Surfaced into the context rather than raised. The corpus needs
			// re-embedding and somebody has to be told; failing the turn does
			// not tell them any better and takes the planner down with it.
			out.Unavailable = mismatch.Error()
			return out, nil
		}
		return out, err
	}

	labels, err := s.labelSources(ctx, q, hits)
	if err != nil {
		return out, err
	}
	for _, h := range hits {
		out.Hits = append(out.Hits, contextbuild.SpillHit{Hit: h, Label: labels[h.SourceID]})
	}
	return out, nil
}

// labelSources turns a source id into the sentence a reader needs to place the
// passage: what it was, on which item, by whom, when.
func (s *Service) labelSources(ctx context.Context, q store.Querier, hits []vector.Hit) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	var worklogs []uuid.UUID
	for _, h := range hits {
		if h.SourceKind == vector.SourceWorklog {
			worklogs = append(worklogs, h.SourceID)
		} else {
			out[h.SourceID] = string(h.SourceKind) + " · " + h.CreatedAt.UTC().Format("2006-01-02")
		}
	}
	if len(worklogs) == 0 {
		return out, nil
	}

	const sql = `
		SELECT w.id, w.kind, coalesce(i.ref, ''), coalesce(u.display_name, coalesce(a.agent_name, '')), w.created_at
		  FROM worklogs w
		  LEFT JOIN work_items i ON i.id = w.work_item_id
		  LEFT JOIN actors a     ON a.id = w.actor_id
		  LEFT JOIN users u      ON u.id = a.user_id
		 WHERE w.id = ANY($1)`
	rows, err := q.Query(ctx, sql, worklogs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id     uuid.UUID
			kind   string
			ref    string
			author string
			at     time.Time
		)
		if err := rows.Scan(&id, &kind, &ref, &author, &at); err != nil {
			return nil, err
		}
		label := "worklog (" + kind + ")"
		if ref != "" {
			label += " on " + ref
		}
		if author != "" {
			label += " · " + author
		}
		out[id] = label + " · " + at.UTC().Format("2006-01-02")
	}
	return out, rows.Err()
}

// ── helpers ──────────────────────────────────────────────────────────────────

func toDecisions(rows []repo.DecisionRow) []contextbuild.Decision {
	out := make([]contextbuild.Decision, 0, len(rows))
	for _, r := range rows {
		out = append(out, contextbuild.Decision{
			ID: r.ID, Title: r.Title, Rationale: r.Rationale,
			DecisionRationale: r.DecisionRationale, Status: r.Status,
			TargetKind: r.TargetKind, DecidedBy: r.DecidedBy, DecidedAt: r.DecidedAt,
		})
	}
	return out
}

// normaliseRef accepts "14" as well as "ZP-14", because that is what people
// type when they are already talking about a project.
func normaliseRef(key, ref string) string {
	if ref == "" {
		return ""
	}
	if onlyDigits(ref) {
		return key + "-" + ref
	}
	return upper(ref)
}

func onlyDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func upper(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - 'a' + 'A'
		}
	}
	return string(out)
}

func appendUnique(list []string, v string) []string {
	if v == "" {
		return list
	}
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}

func sortedIDs(ids []uuid.UUID, loaded map[uuid.UUID]*dg.Entity) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if loaded[id] != nil {
			out = append(out, id)
		}
	}
	// By slug, so the architecture section reads the same on every assembly.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && loaded[out[j]].Slug < loaded[out[j-1]].Slug; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// isNotFound reports the domain's absence sentinel. Used where a reference to
// something that no longer exists is a normal answer: a message naming ZP-14
// after ZP-14 was deleted expands to nothing rather than failing the turn.
func isNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }
