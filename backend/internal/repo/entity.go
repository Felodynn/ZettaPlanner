package repo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Entities is the catalog_entities aggregate — project-level architectural
// identity (§3.4).
type Entities struct{}

const entityColumns = `id, project_id, kind, name, slug, summary, attrs,
                       provenance, actor_id, created_at, updated_at, deleted_at`

// Create inserts an entity.
func (Entities) Create(ctx context.Context, q store.Querier, e *diagram.Entity) error {
	attrs, err := json.Marshal(orEmpty(e.Attrs))
	if err != nil {
		return err
	}
	const sql = `
		INSERT INTO catalog_entities
		  (id, project_id, kind, name, slug, summary, attrs, provenance, actor_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`
	_, err = q.Exec(ctx, sql,
		e.ID, e.ProjectID, string(e.Kind), e.Name, e.Slug, nullIfEmpty(e.Summary),
		attrs, string(e.Provenance), e.ActorID, e.CreatedAt)
	return conflict(err, "an entity with that slug already exists in this project")
}

// ByID returns one live entity.
func (Entities) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*diagram.Entity, error) {
	const sql = `SELECT ` + entityColumns + ` FROM catalog_entities WHERE id = $1 AND deleted_at IS NULL`
	return scanEntity(q.QueryRow(ctx, sql, id))
}

// BySlug returns one live entity by its project-scoped slug.
func (Entities) BySlug(ctx context.Context, q store.Querier, projectID uuid.UUID, slug string) (*diagram.Entity, error) {
	const sql = `SELECT ` + entityColumns + `
	               FROM catalog_entities
	              WHERE project_id = $1 AND slug = $2 AND deleted_at IS NULL`
	return scanEntity(q.QueryRow(ctx, sql, projectID, slug))
}

// ListByProject returns every live entity in a project, ordered the way the
// serializer orders nodes so callers do not have to re-sort.
func (Entities) ListByProject(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]*diagram.Entity, error) {
	const sql = `SELECT ` + entityColumns + `
	               FROM catalog_entities
	              WHERE project_id = $1 AND deleted_at IS NULL
	              ORDER BY kind, slug`
	rows, err := q.Query(ctx, sql, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*diagram.Entity
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FindSimilar returns live entities whose name normalises to the same string as
// name, excluding the given slug.
//
// This is the input to entity resolution, which records a Note and never merges
// (§3.4). Normalisation is done in SQL so the comparison is the same one the
// domain package would make: lower-cased, non-alphanumerics removed.
func (Entities) FindSimilar(ctx context.Context, q store.Querier, projectID uuid.UUID, name, excludeSlug string) ([]*diagram.Entity, error) {
	const sql = `
		SELECT ` + entityColumns + `
		  FROM catalog_entities
		 WHERE project_id = $1
		   AND deleted_at IS NULL
		   AND slug <> $3
		   AND regexp_replace(lower(name), '[^a-z0-9]', '', 'g') = $2
		 ORDER BY created_at`
	rows, err := q.Query(ctx, sql, projectID, diagram.NormaliseName(name), excludeSlug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*diagram.Entity
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Update writes an entity's mutable fields. The slug is not among them: it is
// assigned once and does not follow the name, so a rename is a rename rather
// than a delete plus an add (§6.2).
func (Entities) Update(ctx context.Context, q store.Querier, e *diagram.Entity) error {
	attrs, err := json.Marshal(orEmpty(e.Attrs))
	if err != nil {
		return err
	}
	const sql = `
		UPDATE catalog_entities
		   SET kind = $2, name = $3, summary = $4, attrs = $5, provenance = $6, updated_at = $7
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, e.ID, string(e.Kind), e.Name, nullIfEmpty(e.Summary),
		attrs, string(e.Provenance), time.Now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("entity")
	}
	return nil
}

// Delete soft-deletes an entity.
func (Entities) Delete(ctx context.Context, q store.Querier, id uuid.UUID) error {
	const sql = `UPDATE catalog_entities SET deleted_at = now(), updated_at = now()
	              WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("entity")
	}
	return nil
}

// Placements returns every diagram this entity appears in.
type Placement struct {
	DiagramID   uuid.UUID
	DiagramName string
	DiagramSlug string
	Level       diagram.Level
	NodeID      uuid.UUID
}

// Placements lists where an entity appears across the project. This is what
// makes an entity an identity rather than a box in one picture.
func (Entities) Placements(ctx context.Context, q store.Querier, entityID uuid.UUID) ([]Placement, error) {
	const sql = `
		SELECT d.id, d.name, d.slug, d.level, n.id
		  FROM diagram_nodes n
		  JOIN diagrams d ON d.id = n.diagram_id AND d.deleted_at IS NULL
		 WHERE n.entity_id = $1 AND n.deleted_at IS NULL
		 ORDER BY d.level, d.slug`
	rows, err := q.Query(ctx, sql, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Placement
	for rows.Next() {
		var (
			p     Placement
			level string
		)
		if err := rows.Scan(&p.DiagramID, &p.DiagramName, &p.DiagramSlug, &level, &p.NodeID); err != nil {
			return nil, err
		}
		p.Level = diagram.Level(level)
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanEntity(row scanner) (*diagram.Entity, error) {
	var (
		e          diagram.Entity
		kind       string
		summary    *string
		attrs      []byte
		provenance string
	)
	err := row.Scan(&e.ID, &e.ProjectID, &kind, &e.Name, &e.Slug, &summary, &attrs,
		&provenance, &e.ActorID, &e.CreatedAt, &e.UpdatedAt, &e.DeletedAt)
	if err != nil {
		return nil, notFound(err, "entity")
	}
	e.Kind = diagram.NodeKind(kind)
	e.Provenance = diagram.Provenance(provenance)
	if summary != nil {
		e.Summary = *summary
	}
	if len(attrs) > 0 {
		if err := json.Unmarshal(attrs, &e.Attrs); err != nil {
			return nil, err
		}
	}
	return &e, nil
}
