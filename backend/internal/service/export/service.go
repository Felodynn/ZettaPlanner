// Package export gathers a spec.md and records that it happened.
//
// **Export is manual, always.** Contract §10 defers the Lumix integration to
// v0.1, and there is deliberately no trigger here — no webhook, no on-status
// hook, no scheduled push. A spec that leaves on its own is a spec somebody has
// to un-send, and the thing on the other end is a system that starts work.
//
// The gathering is a read and nothing else. The only write is the audit entry:
// `spec.exported` names the subject and the moment, because "who took this
// outside and when" is exactly the question a retrospective asks about a
// handoff that went wrong, and it is unanswerable afterwards if nobody recorded
// it (§6.5).
package export

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/serialize"
	"github.com/Felodynn/ZettaPlanner/backend/internal/spec"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Service renders spec exports.
type Service struct {
	db  *store.DB
	log *slog.Logger

	projects   repo.Projects
	members    repo.Members
	milestones repo.Milestones
	items      repo.WorkItems
	subtasks   repo.SubTasks
	checklists repo.Checklists
	deps       repo.Dependencies
	refs       repo.EntityRefs
	entities   repo.Entities
	graphs     repo.Graphs
	notes      repo.Notes
	deviations repo.Deviations
	users      repo.Users
}

// New builds the service.
func New(db *store.DB, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, log: log}
}

// Document is a rendered export.
type Document struct {
	Filename string
	Body     []byte
}

// WorkItem exports one work item.
func (s *Service) WorkItem(ctx context.Context, itemID uuid.UUID, p policy.Principal) (*Document, error) {
	item, err := s.items.ByID(ctx, s.db.Pool, itemID)
	if err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, p, item.ProjectID); err != nil {
		return nil, err
	}

	project, err := s.projects.ByID(ctx, s.db.Pool, item.ProjectID)
	if err != nil {
		return nil, err
	}

	rendered, err := s.gatherItem(ctx, project, item)
	if err != nil {
		return nil, err
	}
	ex := spec.Export{
		Kind:       spec.KindWorkItem,
		Project:    projectOf(project),
		ExportedAt: time.Now(),
		ExportedBy: p.DisplayName,
		Item:       &rendered.item,
	}
	if err := s.attachArchitecture(ctx, item.ProjectID, rendered.entityIDs, &ex); err != nil {
		return nil, err
	}

	doc := &Document{Filename: spec.Filename(ex), Body: spec.Render(ex)}
	if err := s.record(ctx, item.ProjectID, audit.Ref(item.ID), p, map[string]any{
		"kind": "work_item", "ref": item.Ref, "bytes": len(doc.Body),
		"entities": slugsOf(rendered.entityIDs, rendered.slugByID),
	}); err != nil {
		return nil, err
	}
	return doc, nil
}

// Milestone exports one milestone and every work item under it.
func (s *Service) Milestone(ctx context.Context, milestoneID uuid.UUID, p policy.Principal) (*Document, error) {
	m, err := s.milestones.ByID(ctx, s.db.Pool, milestoneID)
	if err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, p, m.ProjectID); err != nil {
		return nil, err
	}

	project, err := s.projects.ByID(ctx, s.db.Pool, m.ProjectID)
	if err != nil {
		return nil, err
	}
	items, err := s.items.List(ctx, s.db.Pool, m.ProjectID, repo.WorkItemFilter{MilestoneID: &milestoneID})
	if err != nil {
		return nil, err
	}

	out := spec.Milestone{
		Name:      m.Name,
		Intent:    m.Intent,
		Status:    string(m.Status),
		StartDate: pl.FormatDate(m.StartDate),
		DueDate:   pl.FormatDate(m.DueDate),
	}
	var (
		entityIDs []uuid.UUID
		slugByID  = map[uuid.UUID]string{}
		seen      = map[uuid.UUID]bool{}
	)
	for _, item := range items {
		g, err := s.gatherItem(ctx, project, item)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, g.item)
		for _, id := range g.entityIDs {
			if !seen[id] {
				seen[id] = true
				entityIDs = append(entityIDs, id)
			}
		}
		for id, slug := range g.slugByID {
			slugByID[id] = slug
		}
	}

	ex := spec.Export{
		Kind:       spec.KindMilestone,
		Project:    projectOf(project),
		ExportedAt: time.Now(),
		ExportedBy: p.DisplayName,
		Milestone:  &out,
	}
	if err := s.attachArchitecture(ctx, m.ProjectID, entityIDs, &ex); err != nil {
		return nil, err
	}

	doc := &Document{Filename: spec.Filename(ex), Body: spec.Render(ex)}
	if err := s.record(ctx, m.ProjectID, audit.Ref(m.ID), p, map[string]any{
		"kind": "milestone", "name": m.Name, "items": len(items), "bytes": len(doc.Body),
		"entities": slugsOf(entityIDs, slugByID),
	}); err != nil {
		return nil, err
	}
	return doc, nil
}

// ── gathering ────────────────────────────────────────────────────────────────

type gathered struct {
	item      spec.Item
	entityIDs []uuid.UUID
	slugByID  map[uuid.UUID]string
}

// gatherItem reads everything one work item's section says.
//
// Every read is a plain query against live rows. Nothing is cached between the
// item and its dependencies: a spec assembled from two moments describes a plan
// that never existed.
func (s *Service) gatherItem(ctx context.Context, project *domain.Project, item *pl.WorkItem) (*gathered, error) {
	q := s.db.Pool

	out := spec.Item{
		Ref:         item.Ref,
		Title:       item.Title,
		Intent:      item.Intent,
		Acceptance:  item.Acceptance,
		Status:      string(item.Status),
		Priority:    string(item.Priority),
		ProgressPct: item.ProgressPct,
		StartDate:   pl.FormatDate(item.StartDate),
		DueDate:     pl.FormatDate(item.DueDate),
		Provenance:  string(item.Provenance),
	}

	if item.AssigneeID != nil {
		if u, err := s.users.ByID(ctx, q, *item.AssigneeID); err == nil {
			out.Assignee = u.DisplayName
		} else if !isNotFound(err) {
			return nil, err
		}
	}
	if item.MilestoneID != nil {
		m, err := s.milestones.ByID(ctx, q, *item.MilestoneID)
		switch {
		case err == nil:
			out.Milestone = m.Name
			out.MilestoneDu = pl.FormatDate(m.DueDate)
		case !isNotFound(err):
			return nil, err
		}
	}

	subtasks, err := s.subtasks.ListByItem(ctx, q, item.ID)
	if err != nil {
		return nil, err
	}
	for _, st := range subtasks {
		out.SubTasks = append(out.SubTasks, spec.SubTask{Title: st.Title, Status: string(st.Status)})
	}

	checks, err := s.checklists.ListByItem(ctx, q, item.ID)
	if err != nil {
		return nil, err
	}
	for _, c := range checks {
		out.Checklist = append(out.Checklist, spec.Check{Text: c.Text, Checked: c.Checked})
	}

	// Outgoing `blocks` edges are what this item blocks; incoming ones are what
	// blocks it. Getting the direction backwards would tell a decomposer it can
	// start when it cannot, so both are read from the same helper and labelled
	// once.
	outgoing, err := s.deps.Outgoing(ctx, q, item.ID)
	if err != nil {
		return nil, err
	}
	incoming, err := s.deps.Incoming(ctx, q, item.ID)
	if err != nil {
		return nil, err
	}
	statusOf, err := s.statuses(ctx, outgoing, incoming)
	if err != nil {
		return nil, err
	}
	for _, d := range outgoing {
		link := linkOf(d.ToItemID, d.ToRef, d.ToTitle, statusOf)
		if d.Kind == pl.DepBlocks {
			out.Blocks = append(out.Blocks, link)
		} else {
			out.Relates = append(out.Relates, link)
		}
	}
	for _, d := range incoming {
		link := linkOf(d.FromItemID, d.FromRef, d.FromTitle, statusOf)
		if d.Kind == pl.DepBlocks {
			out.BlockedBy = append(out.BlockedBy, link)
		} else {
			out.Relates = append(out.Relates, link)
		}
	}

	devs, err := s.deviations.ListByProject(ctx, q, project.ID,
		repo.DeviationFilter{WorkItemID: &item.ID, OpenOnly: true})
	if err != nil {
		return nil, err
	}
	for _, d := range devs {
		out.Deviations = append(out.Deviations, spec.Deviation{
			Summary:  d.Summary,
			Severity: string(d.Severity),
			Resolved: d.Resolved(),
			At:       d.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	// And what is open against the things this one is waiting on (D64). One
	// query per unfinished blocker, which is a handful: a blocker's own
	// deviation is the difference between "it lands on the 18th" and "nobody
	// knows when it lands", and the reader has no way to go and look.
	for _, d := range incoming {
		if d.Kind != pl.DepBlocks {
			continue
		}
		blocker, ok := statusOf[d.FromItemID]
		if !ok || blocker.Status == pl.StatusDone || blocker.Status == pl.StatusDropped {
			continue
		}
		open, err := s.deviations.ListByProject(ctx, q, project.ID,
			repo.DeviationFilter{WorkItemID: &blocker.ID, OpenOnly: true})
		if err != nil {
			return nil, err
		}
		for _, dev := range open {
			out.BlockerTrouble = append(out.BlockerTrouble, spec.BlockerIssue{
				Ref: blocker.Ref, Title: blocker.Title,
				Summary: dev.Summary, Severity: string(dev.Severity),
			})
		}
	}

	refs, err := s.refs.ListByItem(ctx, q, item.ID)
	if err != nil {
		return nil, err
	}
	g := &gathered{slugByID: map[uuid.UUID]string{}}
	for _, r := range refs {
		out.EntitySlugs = append(out.EntitySlugs, r.EntitySlug)
		g.entityIDs = append(g.entityIDs, r.EntityID)
		g.slugByID[r.EntityID] = r.EntitySlug
	}
	sort.Strings(out.EntitySlugs)

	g.item = out
	return g, nil
}

// attachArchitecture builds the canonical slice for a set of referenced
// entities: the entities themselves, everything one hop away, the relations
// among them, and what is unresolved or unchecked about any of it.
func (s *Service) attachArchitecture(ctx context.Context, projectID uuid.UUID, referenced []uuid.UUID, ex *spec.Export) error {
	if len(referenced) == 0 {
		return nil
	}
	q := s.db.Pool

	loaded, err := s.entities.ByIDs(ctx, q, referenced)
	if err != nil {
		return err
	}
	hops, err := s.graphs.Hops(ctx, q, projectID, referenced)
	if err != nil {
		return err
	}

	isReferenced := make(map[uuid.UUID]bool, len(referenced))
	for _, id := range referenced {
		isReferenced[id] = true
	}

	var (
		entities  []serialize.SliceEntity
		relations []serialize.SliceRelation
		inSlice   = map[uuid.UUID]bool{}
		sliceIDs  []uuid.UUID
	)
	add := func(e *dg.Entity) {
		if e == nil || inSlice[e.ID] {
			return
		}
		inSlice[e.ID] = true
		sliceIDs = append(sliceIDs, e.ID)
		entities = append(entities, serialize.SliceEntity{
			Slug: e.Slug, Kind: e.Kind, Name: e.Name, Summary: e.Summary,
			Attrs: e.Attrs, Provenance: e.Provenance, Referenced: isReferenced[e.ID],
		})
	}
	for _, id := range referenced {
		add(loaded[id])
	}

	// The far ends of the hops. Loaded in one round trip rather than one query
	// per hop, and only the ones not already in the slice.
	var farIDs []uuid.UUID
	for _, h := range hops {
		if !inSlice[h.OtherID] {
			farIDs = append(farIDs, h.OtherID)
		}
	}
	if len(farIDs) > 0 {
		far, err := s.entities.ByIDs(ctx, q, farIDs)
		if err != nil {
			return err
		}
		for _, id := range farIDs {
			add(far[id])
		}
	}

	slugByID := make(map[uuid.UUID]string, len(sliceIDs))
	for i, id := range sliceIDs {
		slugByID[id] = entities[i].Slug
	}
	for _, h := range hops {
		near, far := slugByID[h.EntityID], slugByID[h.OtherID]
		if near == "" || far == "" {
			continue
		}
		from, to := near, far
		if h.Inbound {
			from, to = far, near
		}
		relations = append(relations, serialize.SliceRelation{
			From: from, To: to, Relation: h.Relation, Label: h.Label, Diagram: h.DiagramSlug,
		})
	}

	body, err := serialize.Slice(entities, relations)
	if err != nil {
		return err
	}
	ex.Architecture = body

	notes, err := s.notes.ForEntities(ctx, q, projectID, sliceIDs)
	if err != nil {
		return err
	}
	for _, n := range notes {
		ex.Notes = append(ex.Notes, spec.Note{
			Kind: string(n.Kind), Subject: n.SubjectSlug, Body: n.Body,
		})
	}

	// Unverified is about the *referenced* entities only. A neighbour nobody has
	// checked is not this item's problem, and listing it puts a caveat on work
	// that is out of scope anyway.
	for _, id := range referenced {
		e := loaded[id]
		if e != nil && e.Provenance == dg.ProvVLMExtracted {
			ex.Unverified = append(ex.Unverified, spec.Unverified{Slug: e.Slug, Name: e.Name})
		}
	}
	return nil
}

// ── plumbing ─────────────────────────────────────────────────────────────────

// record writes the audit entry, in its own transaction.
//
// After the document is rendered rather than before: an export that failed to
// assemble did not happen, and a log saying it did is worse than no log.
func (s *Service) record(ctx context.Context, projectID uuid.UUID, subject *uuid.UUID, p policy.Principal, after map[string]any) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &projectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionSpecExported,
			SubjectID:  subject,
			After:      after,
		})
	})
}

// requireMember is the read gate. A non-member gets not_found rather than
// forbidden, for the same reason every other project-scoped read does:
// distinguishing them lets anyone enumerate what exists.
func (s *Service) requireMember(ctx context.Context, p policy.Principal, projectID uuid.UUID) error {
	if p.GlobalRole == policy.RoleAdmin {
		_, err := s.projects.ByID(ctx, s.db.Pool, projectID)
		return err
	}
	role, err := s.members.RoleFor(ctx, s.db.Pool, projectID, p.UserID)
	if err != nil {
		return err
	}
	if role == nil {
		return fmt.Errorf("%w: project", domain.ErrNotFound)
	}
	return nil
}

// statuses loads the current status and due date of every item named by a
// dependency, so a constraint can say "it is in_progress, due the 20th" rather
// than only that a dependency exists.
func (s *Service) statuses(ctx context.Context, sets ...[]pl.Dependency) (map[uuid.UUID]*pl.WorkItem, error) {
	out := map[uuid.UUID]*pl.WorkItem{}
	for _, set := range sets {
		for _, d := range set {
			for _, id := range []uuid.UUID{d.FromItemID, d.ToItemID} {
				if _, done := out[id]; done {
					continue
				}
				item, err := s.items.ByIDAny(ctx, s.db.Pool, id)
				if err != nil {
					if isNotFound(err) {
						continue
					}
					return nil, err
				}
				out[id] = item
			}
		}
	}
	return out, nil
}

func linkOf(id uuid.UUID, ref, title string, statuses map[uuid.UUID]*pl.WorkItem) spec.Link {
	link := spec.Link{Ref: ref, Title: title}
	if item, ok := statuses[id]; ok {
		link.Status = string(item.Status)
		link.DueDate = pl.FormatDate(item.DueDate)
	}
	return link
}

func projectOf(p *domain.Project) spec.Project {
	return spec.Project{Key: p.Key, Name: p.Name, Summary: p.Description}
}

// isNotFound reports the domain's absence sentinel, for the reads where a
// dangling reference is a normal answer: an item whose milestone was deleted
// still exports.
func isNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }

func slugsOf(ids []uuid.UUID, byID map[uuid.UUID]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if slug := byID[id]; slug != "" {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out
}
