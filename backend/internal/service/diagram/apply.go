package diagram

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
)

// Apply is the additive counterpart to Save.
//
// Save is a human at a canvas sending the complete state: what it does not
// contain, it deletes. Apply is a source of *evidence* — an accepted ingest now,
// an approved proposal in Phase 7 — and evidence is one-directional. A
// photograph of a whiteboard says that these five services exist. It says
// nothing whatsoever about the eleven that were already in the diagram, and
// treating its silence as a deletion would let one badly-lit page destroy an
// afternoon's architecture.
//
// So: no removals, no base revision (there is nothing to be stale against when
// nothing is removed), and no lease. It still produces exactly one revision and
// exactly one audit entry, like every other semantic write.

// NoteInput is a clarification to attach in the same transaction as the graph
// it is about. An ingest that dropped an unresolvable arrow must say so where
// the arrow would have been, not in a log nobody reads.
type NoteInput struct {
	Kind        dg.NoteKind
	Body        string
	SubjectKind string
	SubjectSlug string
}

// ApplyInput is an additive application of semantic state.
type ApplyInput struct {
	// Create makes the diagram as part of the same transaction. Set for an
	// ingest accepted into a new diagram, so a failure part-way leaves no empty
	// diagram behind for somebody to wonder about.
	Create *CreateInput

	// DiagramID names an existing diagram. Ignored when Create is set.
	DiagramID uuid.UUID

	Summary string
	Nodes   []NodeInput
	Edges   []EdgeInput
	Notes   []NoteInput

	// Action is the audit action to record. The caller names it because
	// `ingest.accepted` and `diagram.revised` are different events even though
	// they write the same rows, and a transition recorded as a generic update is
	// a transition the v0.1 export cannot see.
	Action audit.Action

	// AuditAfter is merged into the audit entry's after-state, so the record
	// says which ingest or proposal this came from.
	AuditAfter map[string]any
}

// ApplyResult is what an additive application produced.
type ApplyResult struct {
	DiagramID uuid.UUID
	Diagram   *dg.Diagram
	Revision  int
	Changes   repo.RevisionChanges
	// NotesRaised includes both the entity-resolution overlaps this application
	// produced and the notes the caller asked for.
	NotesRaised []*dg.Note
}

// ApplyInTx runs an additive application inside the caller's transaction.
//
// Exported in this form because the callers own a larger unit of work than one
// diagram: accepting an ingest flips the ingest to `accepted` and creates the
// diagram, and those two must land together or not at all. The repo rule that
// no repository opens a transaction is what makes that composable — the service
// that knows the boundary opens it.
func (s *Service) ApplyInTx(ctx context.Context, tx pgx.Tx, in ApplyInput, p policy.Principal) (*ApplyResult, error) {
	if err := validateInputVocabulary(SaveInput{Nodes: in.Nodes, Edges: in.Edges}); err != nil {
		return nil, err
	}
	if in.Action == "" {
		return nil, fmt.Errorf("%w: an additive apply must name the audit action it records", domain.ErrInvalid)
	}

	d, err := s.resolveTarget(ctx, tx, in, p)
	if err != nil {
		return nil, err
	}

	subject, err := s.requireMember(ctx, tx, p, d.ProjectID)
	if err != nil {
		return nil, err
	}
	// The same authority as a human save. An ingest is not a back door around
	// field ownership: the whole diagram is manager-owned (§4.2), and it goes
	// through the one CheckWrite rather than a second enforcement path.
	if den := policy.CheckWrite(policy.KindDiagram, []string{"nodes", "edges"}, subject); den != nil {
		return nil, den
	}

	entities, raised, changedEntities, err := s.resolveEntities(ctx, tx, d, in.Nodes, p)
	if err != nil {
		return nil, err
	}

	current, err := s.diagrams.LoadGraph(ctx, tx, d.ID)
	if err != nil {
		return nil, err
	}

	nextRevision := d.CurrentRevision + 1
	changes, err := s.applyState(ctx, tx, d, current,
		SaveInput{Summary: in.Summary, Nodes: in.Nodes, Edges: in.Edges},
		entities, changedEntities, p, nextRevision, true)
	if err != nil {
		return nil, err
	}

	result := &ApplyResult{DiagramID: d.ID, Diagram: d, Revision: d.CurrentRevision, Changes: changes, NotesRaised: raised}

	notes, err := s.attachNotes(ctx, tx, d, in.Notes, entities, p)
	if err != nil {
		return nil, err
	}
	result.NotesRaised = append(result.NotesRaised, notes...)

	// A diagram created by this call is a change even when the drawing turned
	// out to be empty, so an empty changeset still gets its revision when the
	// diagram is new. Applying nothing to an *existing* diagram does not.
	if changes.Empty() && in.Create == nil {
		return result, nil
	}

	seq, err := s.diagrams.BumpRevision(ctx, tx, d.ID, d.CurrentRevision)
	if err != nil {
		return nil, err
	}
	if err := s.diagrams.InsertRevision(ctx, tx, &repo.Revision{
		ID: uuid.Must(uuid.NewV7()), DiagramID: d.ID, Seq: seq,
		ActorID: p.WritingActorID(), Summary: in.Summary, Changes: changes, CreatedAt: time.Now(),
	}); err != nil {
		return nil, err
	}
	result.Revision = seq
	d.CurrentRevision = seq

	after := map[string]any{"revision": seq, "summary": in.Summary, "changes": changes}
	for k, v := range in.AuditAfter {
		after[k] = v
	}
	if err := audit.Write(ctx, tx, audit.Entry{
		ProjectID: &d.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
		Action: in.Action, SubjectID: &d.ID,
		After: after,
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// resolveTarget finds or creates the diagram this application lands in.
func (s *Service) resolveTarget(ctx context.Context, tx pgx.Tx, in ApplyInput, p policy.Principal) (*dg.Diagram, error) {
	if in.Create == nil {
		return s.diagrams.ByID(ctx, tx, in.DiagramID)
	}

	c := *in.Create
	if err := dg.ValidateLevel(c.Level); err != nil {
		return nil, err
	}
	slug := c.Slug
	if slug == "" {
		slug = dg.Slugify(c.Name)
	}
	if slug == "" {
		return nil, fmt.Errorf("%w: the diagram needs a name that produces a slug", domain.ErrInvalid)
	}

	subject, err := s.requireMember(ctx, tx, p, c.ProjectID)
	if err != nil {
		return nil, err
	}
	if den := policy.CheckCreate(policy.KindDiagram, subject); den != nil {
		return nil, den
	}

	now := time.Now()
	d := &dg.Diagram{
		ID: uuid.Must(uuid.NewV7()), ProjectID: c.ProjectID,
		Name: c.Name, Slug: slug, Level: c.Level,
		LayoutMode: dg.LayoutAuto, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.diagrams.Create(ctx, tx, d); err != nil {
		return nil, err
	}
	if err := audit.Write(ctx, tx, audit.Entry{
		ProjectID: &c.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
		Action: audit.ActionDiagramCreated, SubjectID: &d.ID,
		After: map[string]any{"slug": d.Slug, "name": d.Name, "level": string(d.Level)},
	}); err != nil {
		return nil, err
	}
	return d, nil
}

// attachNotes records the caller's clarifications against the diagram.
func (s *Service) attachNotes(
	ctx context.Context, tx pgx.Tx, d *dg.Diagram,
	in []NoteInput, entities map[string]*dg.Entity, p policy.Principal,
) ([]*dg.Note, error) {
	var out []*dg.Note
	for _, n := range in {
		if !n.Kind.Valid() {
			return nil, &dg.ValidationError{Message: fmt.Sprintf(
				"note kind %q is not one of overlap, conflict, missing, clarification", n.Kind)}
		}

		note := &dg.Note{
			ID: uuid.Must(uuid.NewV7()), ProjectID: d.ProjectID,
			SubjectKind: "diagram", SubjectID: &d.ID,
			Kind: n.Kind, Body: n.Body, Status: dg.NoteOpen,
			Provenance: dg.ProvSystem, ActorID: p.WritingActorID(), CreatedAt: time.Now(),
			SubjectSlug: d.Slug,
		}
		// A note about a specific box hangs off the entity, so it travels with
		// that identity across every diagram it appears in rather than being
		// stranded on the picture it was first noticed in.
		if n.SubjectSlug != "" {
			if e, ok := entities[n.SubjectSlug]; ok {
				note.SubjectKind = "catalog_entity"
				note.SubjectID = &e.ID
				note.SubjectSlug = e.Slug
			}
		}
		if n.SubjectKind != "" && n.SubjectSlug == "" {
			note.SubjectKind = n.SubjectKind
		}

		if err := s.notes.Create(ctx, tx, note); err != nil {
			return nil, err
		}
		if err := audit.Write(ctx, tx, audit.Entry{
			ProjectID: &d.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionNoteCreated, SubjectID: &note.ID,
			After: map[string]any{"kind": string(note.Kind), "subject": note.SubjectSlug},
		}); err != nil {
			return nil, err
		}
		out = append(out, note)
	}
	return out, nil
}
