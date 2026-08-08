package contextsvc

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/contextbuild"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
)

// Digest returns a project's digest with the harness half recomputed.
//
// **Recomputed on every read**, which is what "cannot drift" means (§6.3). The
// stored row is written back so that somebody reading `project_digest` directly
// sees the same numbers the API does, but nothing depends on the stored copy
// being right — if it were wrong, the next read fixes it and no answer was ever
// based on it.
//
// Staleness of the model half is derived, not remembered: the digest carries
// the project's audit-log high-water mark from when the model fields were
// written, and audit_log is append-only, so "has the graph moved since" is a
// comparison. The alternative is every write path remembering to set a flag,
// which is correct until the fourteenth one.
func (s *Service) Digest(ctx context.Context, projectID uuid.UUID, p policy.Principal) (*contextbuild.Digest, error) {
	var out *contextbuild.Digest
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.requireMember(ctx, tx, p, projectID); err != nil {
			return err
		}

		stored, err := s.digests.Load(ctx, tx, projectID)
		if err != nil {
			return err
		}
		in, err := s.digests.Harness(ctx, tx, projectID)
		if err != nil {
			return err
		}
		mark, err := s.digests.Mark(ctx, tx, projectID)
		if err != nil {
			return err
		}

		stored.Harness = contextbuild.ComputeHarness(in)
		stored.ProjectID = projectID
		// Nothing written yet is not stale. Stale means dated, and a badge over
		// an empty panel teaches people to ignore the badge.
		stored.ModelStale = !stored.Model.Empty() && mark != stored.Revision

		if err := s.digests.SaveHarness(ctx, tx, projectID, stored.Harness, stored.ModelStale); err != nil {
			return err
		}
		// The row was just written, so report that rather than the timestamp
		// read a few statements ago — which is zero the first time through and
		// reads as "this digest is from the year 1".
		stored.UpdatedAt = time.Now()
		out = stored
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetModelFields records the model's hypothesis half.
//
// The only way a model's output reaches project_digest, and it can touch
// nothing else: the statement behind it does not mention harness_fields. In
// LumixAgents Phase 4 a model asked to summarise produced `dead_ends`
// describing role refusals that never happened in that run, reconstructed from
// its own prompt — the fix is not a better prompt, it is that the fields a
// model writes and the fields that are true are different columns.
//
// Phase 6 calls this at the end of a turn. It exists now so the split is
// enforced before there is a model to enforce it against.
func (s *Service) SetModelFields(ctx context.Context, projectID uuid.UUID, m contextbuild.ModelFields, p policy.Principal) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.requireMember(ctx, tx, p, projectID); err != nil {
			return err
		}
		mark, err := s.digests.Mark(ctx, tx, projectID)
		if err != nil {
			return err
		}
		return s.digests.SaveModel(ctx, tx, projectID, m, mark)
	})
}
