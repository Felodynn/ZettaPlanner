package diagram

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/layout"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
)

// The save is atomic and whole-diagram.
//
// The client sends the full semantic state plus the revision it started from.
// If that base has moved, the save is rejected with the diff — never merged.
// One revision per human save is what makes history and the git projection
// readable; a merge that is right most of the time produces a revision log
// nobody can trust and an audit story nobody can follow.

// NodeInput is one node in a submitted state. Identity is the entity slug: a
// diagram is a set of placements of project-level entities, so that is what the
// client names.
type NodeInput struct {
	Slug          string
	Kind          dg.NodeKind
	Name          string
	Summary       string
	Attrs         map[string]any
	Parent        string
	LabelOverride string
	Confidence    *float32
	Provenance    dg.Provenance
}

// EdgeInput is one edge, identified by (from, to, relation).
type EdgeInput struct {
	From       string
	To         string
	Relation   dg.Relation
	Label      string
	Attrs      map[string]any
	Confidence *float32
	Provenance dg.Provenance
}

// SaveInput is a whole-diagram save.
type SaveInput struct {
	BaseRevision int
	Summary      string
	Nodes        []NodeInput
	Edges        []EdgeInput
}

// StaleBaseError reports that the diagram moved under the caller.
//
// It carries the revisions that landed since, so the client can render what
// changed instead of only being told that something did.
type StaleBaseError struct {
	Base    int
	Current int
	Since   []*repo.Revision
}

func (e *StaleBaseError) Error() string {
	return fmt.Sprintf(
		"This diagram is at revision %d and your edit started from %d. %d save(s) landed in between; reload and reapply your change rather than overwriting theirs.",
		e.Current, e.Base, len(e.Since))
}

// SaveResult is what a successful save produced.
type SaveResult struct {
	Revision int
	Changes  repo.RevisionChanges
	// NotesRaised are the entity-resolution clarifications this save produced.
	// Surfaced so the UI can say "we noticed two things that may be the same"
	// at the moment it happened rather than in a list somewhere.
	NotesRaised []*dg.Note
}

// Save applies a whole-diagram state atomically.
func (s *Service) Save(ctx context.Context, diagramID uuid.UUID, in SaveInput, p policy.Principal) (*SaveResult, error) {
	// Vocabulary first, outside the transaction. A closed-set violation is the
	// most common thing a model gets wrong, and the teaching error should come
	// back without having touched the database.
	if err := validateInputVocabulary(in); err != nil {
		return nil, err
	}

	result := &SaveResult{}
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		d, err := s.diagrams.ByID(ctx, tx, diagramID)
		if err != nil {
			return err
		}
		subject, err := s.requireMember(ctx, tx, p, d.ProjectID)
		if err != nil {
			return err
		}
		// The whole diagram is manager-owned (§4.2). The field list is
		// nominal — the table's rule for a diagram is "all" — but it goes
		// through the same CheckWrite as everything else so there is one
		// enforcement path rather than two.
		if den := policy.CheckWrite(policy.KindDiagram, []string{"nodes", "edges"}, subject); den != nil {
			return den
		}

		// The lease, then the base revision. Lease first because "your session
		// went away" is a more actionable thing to hear than "somebody else
		// saved", even when both are true.
		lease, err := s.leases.Get(ctx, tx, diagramID)
		if err != nil {
			return err
		}
		now := time.Now()
		switch {
		case lease == nil:
			return &LeaseLostError{ExpiredAt: now}
		case lease.ActorID != p.WritingActorID():
			if lease.Live(now) {
				return &LeaseHeldError{HolderName: lease.HolderName, HolderKind: lease.HolderKind, ExpiresAt: lease.ExpiresAt}
			}
			return &LeaseLostError{ExpiredAt: lease.ExpiresAt}
		case !lease.Live(now):
			since, err := s.diagrams.RevisionsSince(ctx, tx, diagramID, in.BaseRevision)
			if err != nil {
				return err
			}
			return &LeaseLostError{ExpiredAt: lease.ExpiresAt, Since: since}
		}

		if in.BaseRevision != d.CurrentRevision {
			since, err := s.diagrams.RevisionsSince(ctx, tx, diagramID, in.BaseRevision)
			if err != nil {
				return err
			}
			return &StaleBaseError{Base: in.BaseRevision, Current: d.CurrentRevision, Since: since}
		}

		// Resolve every named entity, creating what is new and raising a Note
		// on anything that looks like a duplicate. Never merging (§3.4).
		entities, notes, err := s.resolveEntities(ctx, tx, d, in.Nodes, p)
		if err != nil {
			return err
		}
		result.NotesRaised = notes

		current, err := s.diagrams.LoadGraph(ctx, tx, diagramID)
		if err != nil {
			return err
		}

		nextRevision := d.CurrentRevision + 1
		changes, err := s.applyState(ctx, tx, d, current, in, entities, p, nextRevision, false)
		if err != nil {
			return err
		}

		// Nothing changed. A revision recording no change is noise in the log
		// and an empty commit in the git projection.
		if changes.Empty() {
			result.Revision = d.CurrentRevision
			result.Changes = changes
			return nil
		}

		// The guarded bump is the concurrency control. Two saves that both
		// passed the base check earlier cannot both land: the second matches no
		// row and the transaction unwinds.
		seq, err := s.diagrams.BumpRevision(ctx, tx, diagramID, d.CurrentRevision)
		if err != nil {
			since, rerr := s.diagrams.RevisionsSince(ctx, tx, diagramID, in.BaseRevision)
			if rerr != nil {
				return rerr
			}
			return &StaleBaseError{Base: in.BaseRevision, Current: d.CurrentRevision + 1, Since: since}
		}

		if err := s.diagrams.InsertRevision(ctx, tx, &repo.Revision{
			ID: uuid.Must(uuid.NewV7()), DiagramID: diagramID, Seq: seq,
			ActorID: p.WritingActorID(), Summary: in.Summary, Changes: changes, CreatedAt: time.Now(),
		}); err != nil {
			return err
		}

		result.Revision = seq
		result.Changes = changes

		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &d.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionDiagramRevised, SubjectID: &diagramID,
			Before: map[string]any{"revision": d.CurrentRevision},
			After:  map[string]any{"revision": seq, "summary": in.Summary, "changes": changes},
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// validateInputVocabulary checks the closed sets before anything is touched.
func validateInputVocabulary(in SaveInput) error {
	for _, n := range in.Nodes {
		if err := dg.ValidateNodeKind(n.Kind); err != nil {
			return err
		}
		if n.Slug == "" {
			return &dg.ValidationError{Message: fmt.Sprintf("node %q has no slug; a node is identified by its entity slug", n.Name)}
		}
	}
	for _, e := range in.Edges {
		if err := dg.ValidateRelation(e.Relation); err != nil {
			return err
		}
	}
	return nil
}

// ── entity resolution ────────────────────────────────────────────────────────

// resolveEntities maps each submitted slug to a catalog entity, creating what
// does not exist yet.
//
// **On a plausible name collision it records a Note and leaves both entities in
// place** (§3.4). It never merges: a merge that is wrong 10% of the time is a
// permanent source of confusion with no permanent fix, and the human who can
// tell the two apart is the one being asked.
func (s *Service) resolveEntities(ctx context.Context, tx pgx.Tx, d *dg.Diagram, nodes []NodeInput, p policy.Principal) (map[string]*dg.Entity, []*dg.Note, error) {
	out := make(map[string]*dg.Entity, len(nodes))
	var raised []*dg.Note

	for _, n := range nodes {
		existing, err := s.entities.BySlug(ctx, tx, d.ProjectID, n.Slug)
		if err != nil && !isNotFound(err) {
			return nil, nil, err
		}

		if existing != nil {
			// The entity is project-level, so a save that renames or re-kinds
			// it changes it everywhere it appears. That is the point of an
			// identity, and it is why the slug never moves.
			if changedEntity(existing, n) {
				existing.Kind = n.Kind
				existing.Name = n.Name
				existing.Summary = n.Summary
				existing.Attrs = n.Attrs
				existing.Provenance = dg.ProvHumanEdited
				if err := s.entities.Update(ctx, tx, existing); err != nil {
					return nil, nil, err
				}
			}
			out[n.Slug] = existing
			continue
		}

		created := &dg.Entity{
			ID: uuid.Must(uuid.NewV7()), ProjectID: d.ProjectID,
			Kind: n.Kind, Name: n.Name, Slug: n.Slug, Summary: n.Summary, Attrs: n.Attrs,
			Provenance: provenanceOr(n.Provenance, dg.ProvHumanCreated),
			ActorID:    p.WritingActorID(), CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := s.entities.Create(ctx, tx, created); err != nil {
			return nil, nil, err
		}
		out[n.Slug] = created

		notes, err := s.noteCollisions(ctx, tx, created, p)
		if err != nil {
			return nil, nil, err
		}
		raised = append(raised, notes...)
	}
	return out, raised, nil
}

// noteCollisions records a Note for each existing entity whose name normalises
// to the same string as the new one.
func (s *Service) noteCollisions(ctx context.Context, tx pgx.Tx, created *dg.Entity, p policy.Principal) ([]*dg.Note, error) {
	similar, err := s.entities.FindSimilar(ctx, tx, created.ProjectID, created.Name, created.Slug)
	if err != nil {
		return nil, err
	}

	var raised []*dg.Note
	for _, other := range similar {
		exists, err := s.notes.ExistsOpenOverlap(ctx, tx, created.ProjectID, created.ID, other.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			continue
		}

		note := &dg.Note{
			ID: uuid.Must(uuid.NewV7()), ProjectID: created.ProjectID,
			SubjectKind: "catalog_entity", SubjectID: &created.ID,
			Kind: dg.NoteOverlap,
			Body: fmt.Sprintf(
				"%q (%s, %s) may be the same thing as %q (%s, %s). They were not merged: only somebody who knows the system can say. "+
					"Resolve this note if they are distinct, or delete one placement and re-point its edges if they are not. [%s]",
				created.Name, created.Kind, created.Slug,
				other.Name, other.Kind, other.Slug, other.ID),
			Status:     dg.NoteOpen,
			Provenance: dg.ProvSystem,
			ActorID:    p.WritingActorID(),
			CreatedAt:  time.Now(),
		}
		if err := s.notes.Create(ctx, tx, note); err != nil {
			return nil, err
		}
		note.SubjectSlug = created.Slug
		raised = append(raised, note)

		if err := audit.Write(ctx, tx, audit.Entry{
			ProjectID: &created.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionNoteCreated, SubjectID: &note.ID,
			After: map[string]any{"kind": string(note.Kind), "subject": created.Slug, "other": other.Slug},
		}); err != nil {
			return nil, err
		}
	}
	return raised, nil
}

func changedEntity(e *dg.Entity, n NodeInput) bool {
	return e.Kind != n.Kind || e.Name != n.Name || e.Summary != n.Summary ||
		!reflect.DeepEqual(orEmptyMap(e.Attrs), orEmptyMap(n.Attrs))
}

// ── diff and apply ───────────────────────────────────────────────────────────

// applyState reconciles the stored graph with the submitted one.
//
// additive suppresses the removal pass: what was submitted is applied on top of
// what is there, and nothing is deleted for being absent. That is the ingest and
// proposal shape — a drawing says what exists, never what does not.
func (s *Service) applyState(
	ctx context.Context, tx pgx.Tx,
	d *dg.Diagram, current *dg.Graph, in SaveInput,
	entities map[string]*dg.Entity, p policy.Principal, revision int, additive bool,
) (repo.RevisionChanges, error) {
	var changes repo.RevisionChanges

	currentNodes := make(map[string]*dg.Node, len(current.Nodes))
	for _, n := range current.Nodes {
		currentNodes[n.Slug()] = n
	}
	desired := make(map[string]NodeInput, len(in.Nodes))
	for _, n := range in.Nodes {
		desired[n.Slug] = n
	}

	// Nodes: create and update first, so the containment parents and the edge
	// endpoints exist before anything references them.
	nodeIDBySlug := make(map[string]uuid.UUID, len(in.Nodes))
	for _, n := range in.Nodes {
		entity := entities[n.Slug]
		if existing, ok := currentNodes[n.Slug]; ok {
			nodeIDBySlug[n.Slug] = existing.ID
			if existing.LabelOverride != n.LabelOverride ||
				!reflect.DeepEqual(orEmptyMap(existing.Attrs), orEmptyMap(n.Attrs)) {
				existing.LabelOverride = n.LabelOverride
				existing.Attrs = n.Attrs
				existing.Provenance = dg.ProvHumanEdited
				if err := s.diagrams.UpdateNode(ctx, tx, existing); err != nil {
					return changes, err
				}
				changes.NodesUpdated = append(changes.NodesUpdated, n.Slug)
			}
			continue
		}

		node := &dg.Node{
			ID: uuid.Must(uuid.NewV7()), DiagramID: d.ID, EntityID: entity.ID,
			LabelOverride: n.LabelOverride, Attrs: n.Attrs,
			Provenance: provenanceOr(n.Provenance, dg.ProvHumanCreated),
			Confidence: n.Confidence, ActorID: p.WritingActorID(), OriginRevision: revision,
		}
		if err := s.diagrams.InsertNode(ctx, tx, node); err != nil {
			return changes, err
		}
		nodeIDBySlug[n.Slug] = node.ID
		changes.NodesAdded = append(changes.NodesAdded, n.Slug)
	}

	// Containment, once every node id is known.
	for _, n := range in.Nodes {
		id := nodeIDBySlug[n.Slug]
		var parentID *uuid.UUID
		if n.Parent != "" {
			pid, ok := nodeIDBySlug[n.Parent]
			if !ok {
				return changes, &dg.ValidationError{Message: fmt.Sprintf(
					"node %s names %s as its containment parent, but %s is not in this diagram",
					n.Slug, n.Parent, n.Parent)}
			}
			parentID = &pid
		}
		existing := currentNodes[n.Slug]
		if existing != nil && sameUUIDPtr(existing.ParentNodeID, parentID) {
			continue
		}
		if err := s.setParent(ctx, tx, id, parentID); err != nil {
			return changes, err
		}
		if existing != nil && !contains(changes.NodesUpdated, n.Slug) && !contains(changes.NodesAdded, n.Slug) {
			changes.NodesUpdated = append(changes.NodesUpdated, n.Slug)
		}
	}

	// Edges.
	currentEdges := make(map[string]*dg.Edge, len(current.Edges))
	for _, e := range current.Edges {
		from, to := current.NodeByID(e.FromNodeID), current.NodeByID(e.ToNodeID)
		if from == nil || to == nil {
			continue
		}
		currentEdges[edgeKey(from.Slug(), to.Slug(), e.Relation)] = e
	}

	desiredEdges := map[string]bool{}
	for _, e := range in.Edges {
		key := edgeKey(e.From, e.To, e.Relation)
		desiredEdges[key] = true

		if existing, ok := currentEdges[key]; ok {
			if existing.Label != e.Label || !reflect.DeepEqual(orEmptyMap(existing.Attrs), orEmptyMap(e.Attrs)) {
				existing.Label = e.Label
				existing.Attrs = e.Attrs
				existing.Provenance = dg.ProvHumanEdited
				if err := s.diagrams.UpdateEdge(ctx, tx, existing); err != nil {
					return changes, err
				}
				changes.EdgesUpdated = append(changes.EdgesUpdated, key)
			}
			continue
		}

		fromID, okFrom := nodeIDBySlug[e.From]
		toID, okTo := nodeIDBySlug[e.To]
		if !okFrom || !okTo {
			missingSlug := e.From
			if okFrom {
				missingSlug = e.To
			}
			return changes, &dg.ValidationError{Message: fmt.Sprintf(
				"edge %s %s %s references %s, which is not one of this diagram's nodes",
				e.From, e.Relation, e.To, missingSlug)}
		}
		edge := &dg.Edge{
			ID: uuid.Must(uuid.NewV7()), DiagramID: d.ID,
			FromNodeID: fromID, ToNodeID: toID, Relation: e.Relation,
			Label: e.Label, Attrs: e.Attrs,
			Provenance: provenanceOr(e.Provenance, dg.ProvHumanCreated),
			Confidence: e.Confidence, ActorID: p.WritingActorID(), OriginRevision: revision,
		}
		if err := s.diagrams.InsertEdge(ctx, tx, edge); err != nil {
			return changes, err
		}
		changes.EdgesAdded = append(changes.EdgesAdded, key)
	}

	if additive {
		return s.finishApply(ctx, tx, d, changes, nodeIDBySlug)
	}

	// Removals. Edges before nodes, so no edge is orphaned even for an instant.
	for key, e := range currentEdges {
		if desiredEdges[key] {
			continue
		}
		if err := s.diagrams.DeleteEdge(ctx, tx, e.ID); err != nil {
			return changes, err
		}
		if err := s.layouts.ClearForEdge(ctx, tx, d.ID, e.ID); err != nil {
			return changes, err
		}
		changes.EdgesRemoved = append(changes.EdgesRemoved, key)
	}
	for slug, n := range currentNodes {
		if _, kept := desired[slug]; kept {
			continue
		}
		if err := s.diagrams.DeleteNode(ctx, tx, n.ID); err != nil {
			return changes, err
		}
		if err := s.layouts.ClearForNode(ctx, tx, d.ID, n.ID); err != nil {
			return changes, err
		}
		changes.NodesRemoved = append(changes.NodesRemoved, slug)
	}

	return s.finishApply(ctx, tx, d, changes, nodeIDBySlug)
}

// finishApply validates what landed and places anything new.
func (s *Service) finishApply(
	ctx context.Context, tx pgx.Tx, d *dg.Diagram,
	changes repo.RevisionChanges, nodeIDBySlug map[string]uuid.UUID,
) (repo.RevisionChanges, error) {
	// Validate the state that actually landed, rather than the state that was
	// asked for. Reloading catches anything the diff logic got wrong as well as
	// anything the input did.
	after, err := s.diagrams.LoadGraph(ctx, tx, d.ID)
	if err != nil {
		return changes, err
	}
	if err := after.Validate(); err != nil {
		return changes, err
	}

	// New arrivals on a manual diagram need somewhere to be. Placed near their
	// neighbours and flagged, so the UI can make them distinct and the user is
	// not hunting for a box at the origin (§3.2).
	if d.LayoutMode == dg.LayoutManual {
		for _, slug := range changes.NodesAdded {
			if err := s.placeUnplaced(ctx, tx, d.ID, nodeIDBySlug[slug]); err != nil {
				return changes, err
			}
		}
	}

	sortChanges(&changes)
	return changes, nil
}

func (s *Service) setParent(ctx context.Context, tx pgx.Tx, nodeID uuid.UUID, parentID *uuid.UUID) error {
	const sql = `UPDATE diagram_nodes SET parent_node_id = $2 WHERE id = $1 AND deleted_at IS NULL`
	_, err := tx.Exec(ctx, sql, nodeID, parentID)
	return err
}

func (s *Service) placeUnplaced(ctx context.Context, tx pgx.Tx, diagramID, nodeID uuid.UUID) error {
	neighbours, err := s.layouts.NeighbourPlacements(ctx, tx, diagramID, nodeID)
	if err != nil {
		return err
	}
	at, ok := layout.Centroid(neighbours)
	if !ok {
		at = layout.Point{}
	}
	return s.layouts.PutUnplaced(ctx, tx, diagramID, nodeID, at)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func edgeKey(from, to string, rel dg.Relation) string {
	return from + " " + string(rel) + " " + to
}

func provenanceOr(p, fallback dg.Provenance) dg.Provenance {
	if p.Valid() {
		return p
	}
	return fallback
}

func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func sameUUIDPtr(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// sortChanges makes the recorded diff deterministic, so the same save always
// produces the same revision row and the git projection's commit message does
// not shuffle between runs.
func sortChanges(c *repo.RevisionChanges) {
	for _, list := range [][]string{
		c.NodesAdded, c.NodesRemoved, c.NodesUpdated,
		c.EdgesAdded, c.EdgesRemoved, c.EdgesUpdated,
	} {
		sort.Strings(list)
	}
}

func isNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }
