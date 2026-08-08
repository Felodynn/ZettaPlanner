package repo

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/vector"
)

// Chunks is the pgvector implementation of vector.Store (CONTRACT §6.4).
//
// It is the only place in this codebase that writes a similarity operator. A
// hand-written `ORDER BY embedding <=> $1` in a service would be a second
// retrieval policy — different filters, different threshold, different
// definition of "relevant" — that nobody finds when the first one changes.
//
// Vectors cross the wire as pgvector's text literal, `[0.1,0.2,…]`, cast with
// `::vector`. That avoids a dependency for what is twenty lines of formatting,
// and the format is pgvector's stable input syntax rather than an internal
// detail.
type Chunks struct {
	q store.Querier
}

// NewChunks binds the store to a querier. Taking it at construction rather than
// per call is what lets this satisfy vector.Store, whose methods take no
// Querier — the interface is the boundary a Milvus implementation would also
// have to meet, and Milvus has no transaction to pass it.
func NewChunks(q store.Querier) *Chunks { return &Chunks{q: q} }

// Upsert writes chunks, replacing any row with the same (kind, source, index).
//
// Called **inside the transaction that wrote the source row** (§6.3): a worklog
// that is committed but not indexed is a worklog that is invisible to
// retrieval, and nothing later notices.
func (c *Chunks) Upsert(ctx context.Context, chunks []vector.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	const sql = `
		INSERT INTO chunks (id, project_id, source_kind, source_id, chunk_idx, content, embedding, model, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::vector, $8, $9)
		ON CONFLICT (source_kind, source_id, chunk_idx) DO UPDATE
		   SET content = EXCLUDED.content,
		       embedding = EXCLUDED.embedding,
		       model = EXCLUDED.model,
		       created_at = EXCLUDED.created_at`

	for _, ch := range chunks {
		if len(ch.Embedding) != vector.Dimensions {
			return &DimensionError{Want: vector.Dimensions, Got: len(ch.Embedding), Model: ch.Model}
		}
		id := ch.ID
		if id == uuid.Nil {
			id = uuid.Must(uuid.NewV7())
		}
		_, err := c.q.Exec(ctx, sql, id, ch.ProjectID, string(ch.SourceKind), ch.SourceID,
			ch.Index, ch.Content, encodeVector(ch.Embedding), ch.Model, ch.CreatedAt)
		if err != nil {
			return err
		}
	}
	return nil
}

// Search returns the nearest chunks within the query's filters.
func (c *Chunks) Search(ctx context.Context, q vector.Query) ([]vector.Hit, error) {
	if len(q.Embedding) != vector.Dimensions {
		return nil, &DimensionError{Want: vector.Dimensions, Got: len(q.Embedding), Model: q.Model}
	}
	if err := c.assertOneSpace(ctx, q.ProjectID, q.Model); err != nil {
		return nil, err
	}

	limit := q.Limit
	if limit <= 0 || limit > 100 {
		limit = 12
	}

	var kinds []string
	for _, k := range q.Kinds {
		kinds = append(kinds, string(k))
	}

	const sql = `
		SELECT id, project_id, source_kind, source_id, chunk_idx, content, model, created_at,
		       1 - (embedding <=> $2::vector) AS score
		  FROM chunks
		 WHERE project_id = $1
		   AND model = $3
		   AND ($4::text[] IS NULL OR source_kind = ANY($4))
		   AND ($5::timestamptz IS NULL OR created_at >= $5)
		   AND ($6::uuid[] IS NULL OR source_id = ANY($6))
		   AND 1 - (embedding <=> $2::vector) >= $7
		 ORDER BY embedding <=> $2::vector, id
		 LIMIT $8`

	rows, err := c.q.Query(ctx, sql, q.ProjectID, encodeVector(q.Embedding), q.Model,
		nilIfEmptyStrings(kinds), q.Since, nilIfEmptyUUIDs(q.SourceIDs), q.MinScore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []vector.Hit
	for rows.Next() {
		var (
			h    vector.Hit
			kind string
		)
		if err := rows.Scan(&h.ID, &h.ProjectID, &kind, &h.SourceID, &h.Index,
			&h.Content, &h.Model, &h.CreatedAt, &h.Score); err != nil {
			return nil, err
		}
		h.SourceKind = vector.SourceKind(kind)
		out = append(out, h)
	}
	return out, rows.Err()
}

// DeleteBySource removes every chunk derived from one source.
func (c *Chunks) DeleteBySource(ctx context.Context, kind vector.SourceKind, id uuid.UUID) error {
	// A chunk is derived data with no lifecycle of its own: it carries no
	// deleted_at, nothing in audit_log points at one, and the source it came
	// from is what the history is about. Keeping tombstones would mean every
	// similarity search filtering them, which is exactly the sort of forgotten
	// predicate that returns deleted text to a model.
	const sql = `DELETE FROM chunks WHERE source_kind = $1 AND source_id = $2` // zp:hard-delete-ok
	_, err := c.q.Exec(ctx, sql, string(kind), id)
	return err
}

// CountByProject reports how much spill is indexed, for the digest and for
// `context show` to say when retrieval found nothing because nothing is there.
func (c *Chunks) CountByProject(ctx context.Context, projectID uuid.UUID) (int, error) {
	const sql = `SELECT count(*) FROM chunks WHERE project_id = $1`
	var n int
	err := c.q.QueryRow(ctx, sql, projectID).Scan(&n)
	return n, err
}

// assertOneSpace refuses a search across two embedding spaces.
//
// An error, not a filter. Filtering would answer a question about ten thousand
// chunks using the eleven that happen to share the model and report success —
// and the answers would be plausible, which is what makes it the worst
// available behaviour.
func (c *Chunks) assertOneSpace(ctx context.Context, projectID uuid.UUID, want string) error {
	const sql = `SELECT DISTINCT model FROM chunks WHERE project_id = $1 ORDER BY model`
	rows, err := c.q.Query(ctx, sql, projectID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var found []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return err
		}
		found = append(found, m)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(found) == 0 {
		return nil // nothing indexed yet; an empty result is the honest answer
	}
	if len(found) == 1 && found[0] == want {
		return nil
	}
	return &vector.ErrModelMismatch{Want: want, Found: found}
}

// DimensionError is a vector of the wrong length reaching the store.
//
// Caught here rather than at the column, because the constraint violation
// Postgres raises names a type and a number and not the thing that actually
// went wrong: somebody changed the embedding model without re-embedding.
type DimensionError struct {
	Want  int
	Got   int
	Model string
}

func (e *DimensionError) Error() string {
	return "the embedding model " + e.Model + " returned a vector of " + strconv.Itoa(e.Got) +
		" dimensions and chunks.embedding holds " + strconv.Itoa(e.Want) +
		". Changing the embedding model requires re-embedding the corpus and a migration for the column; " +
		"storing this would corrupt every subsequent retrieval silently"
}

// encodeVector renders pgvector's input literal.
func encodeVector(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 12)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

func nilIfEmptyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return in
}

func nilIfEmptyUUIDs(in []uuid.UUID) []uuid.UUID {
	if len(in) == 0 {
		return nil
	}
	return in
}
