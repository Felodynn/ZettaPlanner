package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// EntityRefs is the item_entity_refs aggregate: a work item's soft references
// to the architecture graph.
//
// **Set explicitly, never derived** (CONTRACT §3.5). Nothing in this file
// computes a reference from a diagram, a name match, or an embedding. They
// exist so context assembly can pull the relevant architecture slice when an
// item is discussed, and a reference that was guessed pulls the wrong slice
// while looking exactly like one somebody meant.
type EntityRefs struct{}

// Set replaces a work item's references.
func (EntityRefs) Set(ctx context.Context, q store.Querier, itemID uuid.UUID,
	entityIDs []uuid.UUID, relation plan.RefRelation, actorID uuid.UUID) error {

	// A link table with no lifecycle of its own. No audit entry points at a ref
	// row; the edit is recorded as item_entity_refs.set with the before and
	// after sets, which is the fact worth keeping.
	const del = `DELETE FROM item_entity_refs WHERE work_item_id = $1 AND relation = $2` // zp:hard-delete-ok
	if _, err := q.Exec(ctx, del, itemID, string(relation)); err != nil {
		return err
	}
	if len(entityIDs) == 0 {
		return nil
	}

	const ins = `
		INSERT INTO item_entity_refs (work_item_id, entity_id, relation, actor_id)
		SELECT $1, e, $2, $3 FROM unnest($4::uuid[]) AS e
		ON CONFLICT DO NOTHING`
	_, err := q.Exec(ctx, ins, itemID, string(relation), actorID, entityIDs)
	return err
}

// ListByItem returns a work item's references, each naming its entity.
func (EntityRefs) ListByItem(ctx context.Context, q store.Querier, itemID uuid.UUID) ([]plan.EntityRef, error) {
	const sql = `
		SELECT r.work_item_id, r.entity_id, r.relation, r.actor_id, e.slug, e.name
		  FROM item_entity_refs r
		  JOIN catalog_entities e ON e.id = r.entity_id
		 WHERE r.work_item_id = $1 AND e.deleted_at IS NULL
		 ORDER BY e.slug`
	rows, err := q.Query(ctx, sql, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []plan.EntityRef
	for rows.Next() {
		var (
			r        plan.EntityRef
			relation string
		)
		if err := rows.Scan(&r.WorkItemID, &r.EntityID, &relation, &r.ActorID,
			&r.EntitySlug, &r.EntityName); err != nil {
			return nil, err
		}
		r.Relation = plan.RefRelation(relation)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ItemsForEntity is the reverse: which work items claim to touch this piece of
// architecture. Phase 5 assembles context from it; it is here because the join
// belongs with the table.
func (EntityRefs) ItemsForEntity(ctx context.Context, q store.Querier, entityID uuid.UUID) ([]*plan.WorkItem, error) {
	// A var rather than a const: prefixed is a function call, and qualifying
	// the shared column list beats keeping a second aliased copy of it.
	sql := `
		SELECT ` + prefixed("w", workItemColumns) + `
		  FROM work_items w
		  JOIN item_entity_refs r ON r.work_item_id = w.id
		 WHERE r.entity_id = $1 AND w.deleted_at IS NULL
		 ORDER BY w.ref`
	rows, err := q.Query(ctx, sql, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*plan.WorkItem
	for rows.Next() {
		w, err := scanWorkItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ExistingIDs returns the subset of entityIDs that are live entities of a
// project. The caller names the ones missing, rather than letting a foreign key
// produce an error that says only "violates constraint".
func (EntityRefs) ExistingIDs(ctx context.Context, q store.Querier, projectID uuid.UUID, entityIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	const sql = `
		SELECT id FROM catalog_entities
		 WHERE project_id = $1 AND id = ANY($2) AND deleted_at IS NULL`
	rows, err := q.Query(ctx, sql, projectID, entityIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[uuid.UUID]bool, len(entityIDs))
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
