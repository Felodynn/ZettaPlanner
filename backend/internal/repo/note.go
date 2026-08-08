package repo

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Notes is the notes aggregate — the agent's and the harness's channel for
// clarifications: overlaps, conflicts, likely duplicates, missing pieces (§3.4).
//
// Notes live in Postgres and in the assembled context. They are never chunked
// into the vector store: they are structured facts retrieved by traversal, and
// chunking one destroys the structure that made it findable.
type Notes struct{}

const noteColumns = `id, project_id, subject_kind, subject_id, kind, body, status,
                     provenance, actor_id, created_at, resolved_at, deleted_at`

// Create inserts a note.
func (Notes) Create(ctx context.Context, q store.Querier, n *diagram.Note) error {
	const sql = `
		INSERT INTO notes (id, project_id, subject_kind, subject_id, kind, body, status,
		                   provenance, actor_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err := q.Exec(ctx, sql, n.ID, n.ProjectID, n.SubjectKind, n.SubjectID,
		string(n.Kind), n.Body, string(n.Status), string(n.Provenance), n.ActorID, n.CreatedAt)
	return err
}

// ByID returns one live note.
func (Notes) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*diagram.Note, error) {
	const sql = `SELECT ` + noteColumns + ` FROM notes WHERE id = $1 AND deleted_at IS NULL`
	return scanNote(q.QueryRow(ctx, sql, id))
}

// ListByProject returns a project's notes, optionally filtered by status.
func (Notes) ListByProject(ctx context.Context, q store.Querier, projectID uuid.UUID, status diagram.NoteStatus) ([]*diagram.Note, error) {
	const sql = `
		SELECT ` + noteColumns + `
		  FROM notes
		 WHERE project_id = $1 AND deleted_at IS NULL
		   AND ($2 = '' OR status = $2)
		 ORDER BY created_at DESC`
	rows, err := q.Query(ctx, sql, projectID, string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*diagram.Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ForDiagram returns the open notes relevant to a diagram: those about the
// diagram itself, and those about any entity placed in it.
//
// Each carries its subject's slug, so the serialized YAML names the subject
// rather than printing a UUID at a human — or at a model, which has even less
// use for one.
func (Notes) ForDiagram(ctx context.Context, q store.Querier, d *diagram.Diagram, entityIDs []uuid.UUID) ([]*diagram.Note, error) {
	// A var rather than a const: prefixed is a function call, and qualifying the
	// shared column list beats maintaining a second copy of it with an alias.
	sql := `
		SELECT ` + prefixed("n", noteColumns) + `, coalesce(e.slug, '')
		  FROM notes n
		  LEFT JOIN catalog_entities e ON e.id = n.subject_id AND n.subject_kind = 'catalog_entity'
		 WHERE n.project_id = $1
		   AND n.deleted_at IS NULL
		   AND n.status = 'open'
		   AND ( (n.subject_kind = 'diagram' AND n.subject_id = $2)
		      OR (n.subject_kind = 'catalog_entity' AND n.subject_id = ANY($3)) )
		 ORDER BY n.created_at`
	rows, err := q.Query(ctx, sql, d.ProjectID, d.ID, entityIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*diagram.Note
	for rows.Next() {
		n, slug, err := scanNoteWithSlug(rows)
		if err != nil {
			return nil, err
		}
		n.SubjectSlug = slug
		out = append(out, n)
	}
	return out, rows.Err()
}

// ForEntities returns the open notes about any of these catalog entities,
// each carrying its subject's slug.
//
// Deliberately not scoped to a diagram: a spec export bundles the entities a
// work item references, and those may be drawn in four different pictures. The
// question is "what is unresolved about these things", and which picture the
// question was raised in is not part of it.
func (Notes) ForEntities(ctx context.Context, q store.Querier, projectID uuid.UUID, entityIDs []uuid.UUID) ([]*diagram.Note, error) {
	if len(entityIDs) == 0 {
		return nil, nil
	}
	sql := `
		SELECT ` + prefixed("n", noteColumns) + `, coalesce(e.slug, '')
		  FROM notes n
		  JOIN catalog_entities e ON e.id = n.subject_id AND e.deleted_at IS NULL
		 WHERE n.project_id = $1
		   AND n.deleted_at IS NULL
		   AND n.status = 'open'
		   AND n.subject_kind = 'catalog_entity'
		   AND n.subject_id = ANY($2)
		 ORDER BY e.slug, n.created_at`
	rows, err := q.Query(ctx, sql, projectID, entityIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*diagram.Note
	for rows.Next() {
		n, slug, err := scanNoteWithSlug(rows)
		if err != nil {
			return nil, err
		}
		n.SubjectSlug = slug
		out = append(out, n)
	}
	return out, rows.Err()
}

// SetStatus moves a note through its lifecycle.
func (Notes) SetStatus(ctx context.Context, q store.Querier, id uuid.UUID, status diagram.NoteStatus) error {
	const sql = `
		UPDATE notes
		   SET status = $2,
		       resolved_at = CASE WHEN $2 IN ('resolved', 'dismissed') THEN now() ELSE NULL END
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id, string(status))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("note")
	}
	return nil
}

// ExistsOpenOverlap reports whether an open overlap note already links these two
// entities, so that re-saving a diagram does not restate the same clarification
// on every save.
func (Notes) ExistsOpenOverlap(ctx context.Context, q store.Querier, projectID, subjectID, otherID uuid.UUID) (bool, error) {
	const sql = `
		SELECT EXISTS (
		  SELECT 1 FROM notes
		   WHERE project_id = $1
		     AND deleted_at IS NULL
		     AND status = 'open'
		     AND kind = 'overlap'
		     AND subject_kind = 'catalog_entity'
		     AND subject_id IN ($2, $3)
		     AND body LIKE '%' || $4 || '%')`
	var exists bool
	err := q.QueryRow(ctx, sql, projectID, subjectID, otherID, otherID.String()).Scan(&exists)
	return exists, err
}

func scanNote(row scanner) (*diagram.Note, error) {
	var (
		n          diagram.Note
		kind       string
		status     string
		provenance string
	)
	err := row.Scan(&n.ID, &n.ProjectID, &n.SubjectKind, &n.SubjectID, &kind, &n.Body,
		&status, &provenance, &n.ActorID, &n.CreatedAt, &n.ResolvedAt, &n.DeletedAt)
	if err != nil {
		return nil, notFound(err, "note")
	}
	n.Kind = diagram.NoteKind(kind)
	n.Status = diagram.NoteStatus(status)
	n.Provenance = diagram.Provenance(provenance)
	return &n, nil
}

func scanNoteWithSlug(row scanner) (*diagram.Note, string, error) {
	var (
		n          diagram.Note
		kind       string
		status     string
		provenance string
		slug       string
	)
	err := row.Scan(&n.ID, &n.ProjectID, &n.SubjectKind, &n.SubjectID, &kind, &n.Body,
		&status, &provenance, &n.ActorID, &n.CreatedAt, &n.ResolvedAt, &n.DeletedAt, &slug)
	if err != nil {
		return nil, "", notFound(err, "note")
	}
	n.Kind = diagram.NoteKind(kind)
	n.Status = diagram.NoteStatus(status)
	n.Provenance = diagram.Provenance(provenance)
	return &n, slug, nil
}

// prefixed qualifies a bare column list with a table alias, so a joined query
// can reuse the same column constant as the unjoined one.
func prefixed(alias, columns string) string {
	parts := strings.Split(columns, ",")
	for i, col := range parts {
		parts[i] = alias + "." + strings.Join(strings.Fields(col), " ")
	}
	return strings.Join(parts, ", ")
}

// rowsIface is the subset of pgx.Rows the scan helpers need.
type rowsIface interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error
}
