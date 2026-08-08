package plan

import (
	"context"

	"github.com/google/uuid"

	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
)

// Warnings computes a project's date violations.
//
// Computed on every call and stored nowhere. The alternative — a warnings table
// kept up to date by whatever writes a date — is a second source of truth that
// is stale between the write and the reconciliation, and stale in a way nothing
// can see: the row reads identically whether it is true now or was true in
// April.
//
// It is also all that happens. No date is adjusted, no downstream item is
// rescheduled. A cascade that is right 90% of the time silently destroys the
// 10% somebody chose deliberately, and they find out weeks later when the date
// they set has quietly become a different date.
func (s *Service) Warnings(ctx context.Context, projectID uuid.UUID, p policy.Principal) ([]pl.Warning, error) {
	if err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.warnings.ForProject(ctx, s.db.Pool, projectID)
}
