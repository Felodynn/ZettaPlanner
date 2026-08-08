package repo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	ing "github.com/Felodynn/ZettaPlanner/backend/internal/domain/ingest"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Ingests is the ingest aggregate: the upload, its pages, and the merge.
//
// Every state move is a guarded UPDATE naming the state it expects to find.
// Extraction runs in the background and a retry can arrive while it does, so an
// unguarded `SET status = ...` is a lost update waiting for a slow model call —
// and the symptom would be an ingest stuck in `extracting` forever with no
// record of what overwrote what.
type Ingests struct{}

const ingestColumns = `id, project_id, status, source_kind, source_filename, source_path,
                       page_count, diagram_id, actor_id, error_class, error_detail,
                       merged, created_at, updated_at, merged_at, accepted_at`

// Create inserts an ingest.
func (Ingests) Create(ctx context.Context, q store.Querier, i *ing.Ingest) error {
	const sql = `
		INSERT INTO ingests
		  (id, project_id, status, source_kind, source_filename, source_path,
		   page_count, actor_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`
	_, err := q.Exec(ctx, sql,
		i.ID, i.ProjectID, string(i.Status), string(i.SourceKind),
		nullIfEmpty(i.SourceFilename), nullIfEmpty(i.SourcePath),
		i.PageCount, i.ActorID, i.CreatedAt)
	return err
}

// ByID returns one ingest without its pages.
func (Ingests) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*ing.Ingest, error) {
	const sql = `SELECT ` + ingestColumns + ` FROM ingests WHERE id = $1`
	return scanIngest(q.QueryRow(ctx, sql, id))
}

// WithPages returns one ingest and every page, in index order.
func (r Ingests) WithPages(ctx context.Context, q store.Querier, id uuid.UUID) (*ing.Ingest, error) {
	i, err := r.ByID(ctx, q, id)
	if err != nil {
		return nil, err
	}
	pages, err := r.Pages(ctx, q, id)
	if err != nil {
		return nil, err
	}
	i.Pages = pages
	return i, nil
}

// ListByProject returns a project's ingests, newest first.
func (Ingests) ListByProject(ctx context.Context, q store.Querier, projectID uuid.UUID, limit int) ([]*ing.Ingest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const sql = `SELECT ` + ingestColumns + `
	               FROM ingests WHERE project_id = $1
	              ORDER BY created_at DESC LIMIT $2`
	rows, err := q.Query(ctx, sql, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*ing.Ingest
	for rows.Next() {
		i, err := scanIngest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// ForDiagram returns the accepted ingests a diagram was built from, newest
// first.
//
// This is what makes re-evaluate worth running: the pages are the evidence, and
// showing the model the drawing again alongside what a human corrected
// afterwards is a much sharper question than showing it the graph alone (§5.4).
func (Ingests) ForDiagram(ctx context.Context, q store.Querier, diagramID uuid.UUID, limit int) ([]*ing.Ingest, error) {
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	const sql = `SELECT ` + ingestColumns + `
	               FROM ingests
	              WHERE diagram_id = $1 AND status = 'accepted'
	              ORDER BY created_at DESC LIMIT $2`
	rows, err := q.Query(ctx, sql, diagramID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*ing.Ingest
	for rows.Next() {
		i, err := scanIngest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Transition moves an ingest from one state to another, refusing if it is no
// longer in the state the caller last saw.
//
// Returns false rather than an error when the guard misses: a lost race is a
// normal outcome for background work, and the caller decides whether this one
// mattered.
func (Ingests) Transition(ctx context.Context, q store.Querier, id uuid.UUID, from, to ing.Status) (bool, error) {
	const sql = `UPDATE ingests SET status = $3, updated_at = now()
	              WHERE id = $1 AND status = $2`
	tag, err := q.Exec(ctx, sql, id, string(from), string(to))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Fail records a terminal failure with its class.
func (Ingests) Fail(ctx context.Context, q store.Querier, id uuid.UUID, class, detail string) error {
	const sql = `UPDATE ingests SET status = 'failed', error_class = $2, error_detail = $3, updated_at = now()
	              WHERE id = $1 AND status <> 'accepted'`
	tag, err := q.Exec(ctx, sql, id, nullIfEmpty(class), nullIfEmpty(detail))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("ingest to fail")
	}
	return nil
}

// SetMerged stores the merge step's output and moves the ingest to review.
//
// Written rather than recomputed on read: the merge rules will change, and an
// ingest a human approved must still show what they approved.
func (Ingests) SetMerged(ctx context.Context, q store.Querier, id uuid.UUID, m *ing.Merged) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	const sql = `UPDATE ingests SET merged = $2, merged_at = now(), updated_at = now() WHERE id = $1`
	tag, err := q.Exec(ctx, sql, id, payload)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("ingest")
	}
	return nil
}

// Accept records the diagram an ingest produced.
//
// The status guard is the concurrency control on accept: two reviewers clicking
// at once cannot both create a diagram, because the second one's UPDATE matches
// no row and its transaction unwinds.
func (Ingests) Accept(ctx context.Context, q store.Querier, id, diagramID uuid.UUID) (bool, error) {
	const sql = `UPDATE ingests
	                SET status = 'accepted', diagram_id = $2, accepted_at = now(), updated_at = now()
	              WHERE id = $1 AND status = 'review'`
	tag, err := q.Exec(ctx, sql, id, diagramID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func scanIngest(row scanner) (*ing.Ingest, error) {
	var (
		i          ing.Ingest
		status     string
		sourceKind string
		filename   *string
		sourcePath *string
		errClass   *string
		errDetail  *string
		merged     []byte
	)
	err := row.Scan(&i.ID, &i.ProjectID, &status, &sourceKind, &filename, &sourcePath,
		&i.PageCount, &i.DiagramID, &i.ActorID, &errClass, &errDetail,
		&merged, &i.CreatedAt, &i.UpdatedAt, &i.MergedAt, &i.AcceptedAt)
	if err != nil {
		return nil, notFound(err, "ingest")
	}

	i.Status = ing.Status(status)
	i.SourceKind = ing.SourceKind(sourceKind)
	i.SourceFilename = deref(filename)
	i.SourcePath = deref(sourcePath)
	i.ErrorClass = deref(errClass)
	i.ErrorDetail = deref(errDetail)
	if len(merged) > 0 {
		var m ing.Merged
		if err := json.Unmarshal(merged, &m); err != nil {
			return nil, err
		}
		i.Merged = &m
	}
	return &i, nil
}

// ── pages ────────────────────────────────────────────────────────────────────

const pageColumns = `id, ingest_id, page_idx, image_path, status, extracted, raw_output, model,
                     tokens_in, tokens_out, attempts, error_class, error_detail,
                     duration_ms, extracted_at`

// InsertPage adds one rasterized page.
func (Ingests) InsertPage(ctx context.Context, q store.Querier, p *ing.Page) error {
	const sql = `
		INSERT INTO ingest_pages (id, ingest_id, page_idx, image_path, status)
		VALUES ($1, $2, $3, $4, $5)`
	_, err := q.Exec(ctx, sql, p.ID, p.IngestID, p.Idx, p.ImagePath, string(p.Status))
	return conflict(err, "that page index already exists on this ingest")
}

// Pages returns an ingest's pages in index order.
func (Ingests) Pages(ctx context.Context, q store.Querier, ingestID uuid.UUID) ([]*ing.Page, error) {
	const sql = `SELECT ` + pageColumns + ` FROM ingest_pages WHERE ingest_id = $1 ORDER BY page_idx`
	rows, err := q.Query(ctx, sql, ingestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*ing.Page
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClaimPage takes a page for extraction, refusing one already in flight.
//
// The guard is what makes a retry safe to fire while the first attempt is still
// running: only one claim wins, and the loser does nothing rather than issuing a
// second model call against the same image.
func (Ingests) ClaimPage(ctx context.Context, q store.Querier, ingestID uuid.UUID, idx int) (bool, error) {
	const sql = `
		UPDATE ingest_pages
		   SET status = 'extracting', attempts = attempts + 1, error_class = NULL, error_detail = NULL
		 WHERE ingest_id = $1 AND page_idx = $2 AND status IN ('pending', 'extracted', 'failed')`
	tag, err := q.Exec(ctx, sql, ingestID, idx)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SavePageExtraction stores what the model returned, verbatim.
func (Ingests) SavePageExtraction(ctx context.Context, q store.Querier, ingestID uuid.UUID, idx int, p *ing.Page) error {
	payload, err := json.Marshal(p.Extracted)
	if err != nil {
		return err
	}
	const sql = `
		UPDATE ingest_pages
		   SET status = 'extracted', extracted = $3, raw_output = $4, model = $5,
		       tokens_in = $6, tokens_out = $7, duration_ms = $8,
		       extracted_at = now(), error_class = NULL, error_detail = NULL
		 WHERE ingest_id = $1 AND page_idx = $2`
	tag, err := q.Exec(ctx, sql, ingestID, idx, payload, nullIfEmpty(p.Raw), nullIfEmpty(p.Model),
		p.TokensIn, p.TokensOut, p.DurationMS)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("ingest page")
	}
	return nil
}

// FailPage records that one page could not be read.
//
// A failed page fails the page, never the ingest. Four readable pages and one
// smudged one is a reviewable result; refusing the whole upload over it would
// make the retry that exists for exactly this case pointless.
// The raw answer is written here too. A malformed answer is the one you most
// want to read, and it is the one that leaves nothing in `extracted`.
func (Ingests) FailPage(ctx context.Context, q store.Querier, ingestID uuid.UUID, idx int, class, detail, raw string) error {
	const sql = `
		UPDATE ingest_pages
		   SET status = 'failed', error_class = $3, error_detail = $4, raw_output = $5, extracted_at = now()
		 WHERE ingest_id = $1 AND page_idx = $2`
	tag, err := q.Exec(ctx, sql, ingestID, idx, nullIfEmpty(class), nullIfEmpty(detail), nullIfEmpty(raw))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("ingest page")
	}
	return nil
}

// Unfinished returns ingests left mid-flight, oldest first.
//
// Read at boot. A process killed during extraction leaves rows saying work is
// in progress that no longer is, and an ingest stuck in `extracting` forever
// with no explanation is worse than one marked failed with a reason.
func (Ingests) Unfinished(ctx context.Context, q store.Querier, olderThan time.Duration) ([]*ing.Ingest, error) {
	const sql = `SELECT ` + ingestColumns + `
	               FROM ingests
	              WHERE status IN ('uploaded', 'extracting', 'merging')
	                AND updated_at < now() - $1::interval
	              ORDER BY created_at`
	rows, err := q.Query(ctx, sql, olderThan.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*ing.Ingest
	for rows.Next() {
		i, err := scanIngest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func scanPage(row scanner) (*ing.Page, error) {
	var (
		p         ing.Page
		status    string
		extracted []byte
		raw       *string
		model     *string
		tokensIn  *int
		tokensOut *int
		errClass  *string
		errDetail *string
		duration  *int
	)
	err := row.Scan(&p.ID, &p.IngestID, &p.Idx, &p.ImagePath, &status, &extracted, &raw, &model,
		&tokensIn, &tokensOut, &p.Attempts, &errClass, &errDetail, &duration, &p.ExtractedAt)
	if err != nil {
		return nil, notFound(err, "ingest page")
	}

	p.Status = ing.PageStatus(status)
	p.Raw = deref(raw)
	p.Model = deref(model)
	p.ErrorClass = deref(errClass)
	p.ErrorDetail = deref(errDetail)
	if tokensIn != nil {
		p.TokensIn = *tokensIn
	}
	if tokensOut != nil {
		p.TokensOut = *tokensOut
	}
	if duration != nil {
		p.DurationMS = *duration
	}
	if len(extracted) > 0 {
		var e ing.PageExtraction
		if err := json.Unmarshal(extracted, &e); err != nil {
			return nil, err
		}
		p.Extracted = &e
	}
	return &p, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
