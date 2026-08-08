// Package vector is the similarity-search boundary (CONTRACT §6.4).
//
// pgvector is the only implementation in v0 (D1). The interface exists so that
// a Milvus implementation is a package rather than a refactor, and so that
// **no caller may reach past it into SQL for similarity**. A hand-written
// `ORDER BY embedding <=> $1` somewhere in a service is not a shortcut, it is a
// second retrieval policy that nobody will find when the first one changes.
//
// Nothing here does I/O. The types are values a store moves in and out; the
// store itself lives in internal/repo, and the assembly that consumes hits
// lives in internal/contextbuild, which cannot reach either.
package vector

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SourceKind is the closed set of things that may be chunked.
//
// **Only spill is chunked** (§6.3 layer 3): worklogs, past messages, imported
// documents. Structured objects — work items, notes, proposals, entities — are
// retrieved by query, because chunking a note destroys the structure that made
// it findable. `note` is in the schema's CHECK from 0001 and is deliberately
// absent here: the column permits it, this package does not offer it, and the
// difference is the point.
type SourceKind string

const (
	SourceWorklog  SourceKind = "worklog"
	SourceMessage  SourceKind = "message"
	SourceDocument SourceKind = "document"
)

// SourceKinds returns every chunkable kind.
func SourceKinds() []SourceKind { return []SourceKind{SourceWorklog, SourceMessage, SourceDocument} }

// Valid reports whether k may be chunked.
func (k SourceKind) Valid() bool {
	switch k {
	case SourceWorklog, SourceMessage, SourceDocument:
		return true
	}
	return false
}

func (k SourceKind) String() string { return string(k) }

// Chunk is one embedded fragment of unstructured text.
//
// Model is recorded per row rather than per deployment. Changing the embedding
// model requires re-embedding the corpus, and a corpus half in one space and
// half in another returns plausible neighbours that are simply wrong — the
// failure mode with no error message. Search refuses a mixed space rather than
// averaging over it.
type Chunk struct {
	ID         uuid.UUID
	ProjectID  uuid.UUID
	SourceKind SourceKind
	SourceID   uuid.UUID
	Index      int
	Content    string
	Embedding  []float32
	Model      string
	CreatedAt  time.Time
}

// Query is a similarity search, always scoped to one project and one embedding
// space.
type Query struct {
	ProjectID uuid.UUID
	// Embedding is the query vector. Produced by the same model named in Model.
	Embedding []float32
	// Model must match the model recorded on the rows. A mismatch is an error
	// (ErrModelMismatch), never a silent search across two vector spaces.
	Model string

	// Kinds restricts which spill is searched. Empty means all of it.
	Kinds []SourceKind
	// Since restricts to chunks created at or after an instant. Spill is
	// usually filtered by date or by item; an unfiltered similarity search over
	// a year of worklogs answers with whatever was most eloquent.
	Since *time.Time
	// SourceIDs restricts to specific sources, which is how "this item's
	// worklogs" is asked.
	SourceIDs []uuid.UUID

	Limit int
	// MinScore drops weak neighbours. Cosine similarity in [0,1]; a hit at 0.2
	// is noise that costs context budget and misleads a reader who assumes
	// everything present was relevant.
	MinScore float32
}

// Hit is one retrieved chunk with its similarity.
type Hit struct {
	Chunk
	// Score is cosine similarity in [0,1]. Higher is closer.
	Score float32
}

// Store is the interface held open by CONTRACT §6.4.
type Store interface {
	// Upsert writes chunks, replacing any existing rows for the same
	// (source_kind, source_id, chunk_idx).
	Upsert(ctx context.Context, chunks []Chunk) error
	// Search returns the nearest chunks to q.Embedding within q's filters.
	Search(ctx context.Context, q Query) ([]Hit, error)
	// DeleteBySource removes every chunk derived from one source, which is how
	// a re-indexed source stops carrying its previous shape.
	DeleteBySource(ctx context.Context, kind SourceKind, id uuid.UUID) error
}

// ErrModelMismatch is returned when a query's embedding model does not match
// the model the stored rows were embedded with.
//
// An error rather than a filter. Filtering would answer a query about a corpus
// of ten thousand chunks using the eleven that happen to share the model, and
// report success.
type ErrModelMismatch struct {
	Want  string
	Found []string
}

func (e *ErrModelMismatch) Error() string {
	return fmt.Sprintf(
		"this project's chunks were embedded with %v and the query used %q. "+
			"Searching across two embedding spaces returns plausible neighbours that are simply wrong, "+
			"so it is refused: re-embed the corpus with the configured model, or configure the model it was embedded with",
		e.Found, e.Want)
}

// Dimensions reports the length a vector must have to be stored. Asserted
// against the live embedding model at boot and against the chunks.embedding
// column; a silent dimension change corrupts every subsequent retrieval (§8).
const Dimensions = 1024
