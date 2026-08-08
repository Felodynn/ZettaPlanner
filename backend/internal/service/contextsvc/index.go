package contextsvc

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/contextbuild"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/vector"
)

// IndexWorklog chunks and embeds a worklog inside the transaction that wrote it.
//
// **Inside the transaction, on purpose** (§6.3). The alternative — commit now,
// index shortly — produces a worklog that exists and is invisible to retrieval,
// and nothing downstream can tell that state from "there was nothing relevant".
// A backfill job would make it a second source of truth about what has been
// indexed, which is the shape of bug this whole layer exists to avoid.
//
// The cost is real and worth naming: a worklog write now depends on the
// embedding model answering. If it does not, **the write fails** rather than
// landing unindexed. That is why models.yaml keeps embeddings local and without
// a cloud fallback — a network round trip to another host inside an open
// transaction holds locks for the length of that round trip.
func (s *Service) IndexWorklog(ctx context.Context, q store.Querier, w *pl.Worklog) error {
	return s.index(ctx, q, vector.SourceWorklog, w.ProjectID, w.ID, w.Body)
}

// IndexMessage is the same path for conversation spill. Wired here so Phase 6
// adds a call rather than a mechanism.
func (s *Service) IndexMessage(ctx context.Context, q store.Querier, projectID, messageID uuid.UUID, body string) error {
	return s.index(ctx, q, vector.SourceMessage, projectID, messageID, body)
}

func (s *Service) index(ctx context.Context, q store.Querier, kind vector.SourceKind,
	projectID, sourceID uuid.UUID, body string) error {

	client, why := s.embedder()
	if client == nil {
		// Not configured is not the same as broken. A deployment with no
		// embedding model still writes plans; what it does not get is layer 3,
		// and the assembled context says exactly that instead of showing an
		// empty section.
		s.log.DebugContext(ctx, "spill not indexed", "reason", why, "kind", kind, "source", sourceID)
		return nil
	}

	pieces := contextbuild.Chunk(body)
	if len(pieces) == 0 {
		return nil
	}

	res, err := client.Embed(ctx, pieces...)
	if err != nil {
		return fmt.Errorf("embedding this %s failed, so it was not saved: saving it unindexed would make it "+
			"invisible to the planner with nothing to show that it had happened: %w", kind, err)
	}

	store := repo.NewChunks(q)
	// Replace rather than merge. A re-indexed source that kept its old chunks
	// would answer searches with text that is no longer there.
	if err := store.DeleteBySource(ctx, kind, sourceID); err != nil {
		return err
	}

	chunks := make([]vector.Chunk, 0, len(pieces))
	for i, piece := range pieces {
		chunks = append(chunks, vector.Chunk{
			ID:         uuid.Must(uuid.NewV7()),
			ProjectID:  projectID,
			SourceKind: kind,
			SourceID:   sourceID,
			Index:      i,
			Content:    piece,
			Embedding:  res.Embeddings[i],
			// Recorded per row, not per deployment. Changing the embedding
			// model requires re-embedding the corpus, and this column is what
			// makes a half-migrated corpus an error instead of a silent
			// mixed-space search.
			Model:     client.Config().Model,
			CreatedAt: time.Now(),
		})
	}
	return store.Upsert(ctx, chunks)
}
