package repo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/contextbuild"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Digests is the project_digest aggregate.
//
// This is the one file in internal/repo that imports internal/contextbuild, and
// the direction is deliberate: contextbuild is pure — no I/O, no clock it did
// not receive — so it behaves as a domain package here, and it owns the digest's
// shape because the harness/model split *is* a context concern. Persisting a
// shape defined elsewhere is what every other file in this package does too.
//
// The table stores two halves that must never touch. This file enforces that
// structurally rather than by convention: **there is no method that writes both
// columns.** `SaveHarness` cannot see model fields and `SaveModel` cannot see
// harness fields, so a caller that wanted to let a model write a count would
// have to add a method to do it, which is a change somebody reviews.
type Digests struct{}

// Mark is a project's audit-log high-water mark.
//
// audit_log is append-only and nothing is ever deleted from it (§6.5), so this
// count only goes up, and "has anything happened since the model last looked"
// is a comparison rather than a flag somebody has to remember to set on every
// one of fourteen write paths.
func (Digests) Mark(ctx context.Context, q store.Querier, projectID uuid.UUID) (int, error) {
	const sql = `SELECT count(*) FROM audit_log WHERE project_id = $1`
	var n int
	err := q.QueryRow(ctx, sql, projectID).Scan(&n)
	return n, err
}

// Load returns the stored digest, or a zero digest when none has been written.
//
// The harness half in the row is a cache. Every read path recomputes it — see
// Harness below — so a stale row cannot mislead anybody; it exists so that
// somebody reading the table directly sees the same numbers the API does.
func (Digests) Load(ctx context.Context, q store.Querier, projectID uuid.UUID) (*contextbuild.Digest, error) {
	const sql = `
		SELECT revision, harness_fields, model_fields, model_fields_stale, updated_at
		  FROM project_digest WHERE project_id = $1`

	var (
		d          = contextbuild.Digest{ProjectID: projectID}
		harnessRaw []byte
		modelRaw   []byte
	)
	err := q.QueryRow(ctx, sql, projectID).Scan(&d.Revision, &harnessRaw, &modelRaw, &d.ModelStale, &d.UpdatedAt)
	if isNoRows(err) {
		return &contextbuild.Digest{ProjectID: projectID}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(harnessRaw) > 0 {
		if err := json.Unmarshal(harnessRaw, &d.Harness); err != nil {
			return nil, err
		}
	}
	if len(modelRaw) > 0 {
		if err := json.Unmarshal(modelRaw, &d.Model); err != nil {
			return nil, err
		}
	}
	return &d, nil
}

// SaveHarness writes the computed half and the current staleness verdict.
//
// It cannot write model fields: `model_fields` is not in the statement, and the
// insert path seeds it with an empty object exactly once.
func (Digests) SaveHarness(ctx context.Context, q store.Querier, projectID uuid.UUID, h contextbuild.HarnessFields, stale bool) error {
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	const sql = `
		INSERT INTO project_digest (project_id, revision, harness_fields, model_fields, model_fields_stale, updated_at)
		VALUES ($1, 0, $2, '{}'::jsonb, $3, now())
		ON CONFLICT (project_id) DO UPDATE
		   SET harness_fields = EXCLUDED.harness_fields,
		       model_fields_stale = EXCLUDED.model_fields_stale,
		       updated_at = now()`
	_, err = q.Exec(ctx, sql, projectID, raw, stale)
	return err
}

// SaveModel writes the hypothesis half and stamps it with the mark it was
// written against.
//
// It cannot write harness fields. That is the whole point: in LumixAgents Phase
// 4 a model asked to summarise produced `dead_ends` describing events that
// never happened, reconstructed from its own prompt — and the fix is not a
// better prompt, it is that the fields a model writes and the fields that are
// true are different columns written by different statements.
func (Digests) SaveModel(ctx context.Context, q store.Querier, projectID uuid.UUID, m contextbuild.ModelFields, mark int) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	const sql = `
		INSERT INTO project_digest (project_id, revision, harness_fields, model_fields, model_fields_stale, updated_at)
		VALUES ($1, $3, '{}'::jsonb, $2, false, now())
		ON CONFLICT (project_id) DO UPDATE
		   SET model_fields = EXCLUDED.model_fields,
		       revision = EXCLUDED.revision,
		       model_fields_stale = false,
		       updated_at = now()`
	_, err = q.Exec(ctx, sql, projectID, raw, mark)
	return err
}

// Harness recomputes the whole computed half from the database.
//
// Recomputed on every read, which is what "cannot drift" means. The alternative
// — maintaining counts incrementally as things change — is correct until the
// first write path that forgets, and the failure is a number that is merely
// wrong rather than obviously missing.
func (Digests) Harness(ctx context.Context, q store.Querier, projectID uuid.UUID) (contextbuild.HarnessInput, error) {
	in := contextbuild.HarnessInput{
		ByStatus:       map[string]int{},
		Deviations:     map[string]int{},
		ClosedStatuses: plan.ClosedStatuses(),
	}

	// One statement per shape of question rather than one heroic query. These
	// run on indexed columns against a project's rows, and a query somebody can
	// read is worth more here than a query that saves four round trips.
	const byStatus = `
		SELECT status, count(*) FROM work_items
		 WHERE project_id = $1 AND deleted_at IS NULL GROUP BY status`
	rows, err := q.Query(ctx, byStatus, projectID)
	if err != nil {
		return in, err
	}
	for rows.Next() {
		var (
			status string
			n      int
		)
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return in, err
		}
		in.ByStatus[status] = n
		in.WorkItems += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return in, err
	}

	const blocked = `
		SELECT ref FROM work_items
		 WHERE project_id = $1 AND deleted_at IS NULL AND status = $2 ORDER BY ref`
	if in.BlockedRefs, err = queryStrings(ctx, q, blocked, projectID, string(plan.StatusBlocked)); err != nil {
		return in, err
	}

	const unassigned = `
		SELECT count(*) FROM work_items
		 WHERE project_id = $1 AND deleted_at IS NULL AND assignee_id IS NULL
		   AND status <> ALL($2::text[])`
	if err := q.QueryRow(ctx, unassigned, projectID, plan.ClosedStatuses()).Scan(&in.UnassignedOpen); err != nil {
		return in, err
	}

	const deviations = `
		SELECT severity, count(*) FROM deviations
		 WHERE project_id = $1 AND deleted_at IS NULL AND resolved_at IS NULL GROUP BY severity`
	rows, err = q.Query(ctx, deviations, projectID)
	if err != nil {
		return in, err
	}
	for rows.Next() {
		var (
			severity string
			n        int
		)
		if err := rows.Scan(&severity, &n); err != nil {
			rows.Close()
			return in, err
		}
		in.Deviations[severity] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return in, err
	}

	const scalars = `
		SELECT (SELECT count(*) FROM notes            WHERE project_id = $1 AND deleted_at IS NULL AND status = 'open'),
		       (SELECT count(*) FROM diagrams         WHERE project_id = $1 AND deleted_at IS NULL),
		       (SELECT count(*) FROM catalog_entities WHERE project_id = $1 AND deleted_at IS NULL),
		       (SELECT count(*) FROM chunks           WHERE project_id = $1),
		       (SELECT coalesce(string_agg(name, ', ' ORDER BY seq), '') FROM milestones
		         WHERE project_id = $1 AND deleted_at IS NULL AND status = 'active'),
		       (SELECT max(at) FROM audit_log WHERE project_id = $1)`
	var last *time.Time
	if err := q.QueryRow(ctx, scalars, projectID).Scan(
		&in.OpenNotes, &in.Diagrams, &in.Entities, &in.IndexedChunks, &in.ActiveMilestone, &last); err != nil {
		return in, err
	}
	in.LastActivityAt = last

	const titles = `
		SELECT title FROM proposals
		 WHERE project_id = $1 AND status = $2
		 ORDER BY coalesce(decided_at, created_at) DESC LIMIT 5`
	if in.RecentDecisions, err = queryStrings(ctx, q, titles, projectID, "approved"); err != nil {
		return in, err
	}
	if in.RejectedRecent, err = queryStrings(ctx, q, titles, projectID, "rejected"); err != nil {
		return in, err
	}
	return in, nil
}

func queryStrings(ctx context.Context, q store.Querier, sql string, args ...any) ([]string, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
