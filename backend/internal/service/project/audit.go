package project

import (
	"context"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
)

// Audit returns a project's audit entries.
//
// **Readable by any member of the project, not only by managers.** This is the
// same call §3.6 makes about the conversation: everything anyone does in a
// project is visible to everyone in it, including managers reading developers'
// turns and developers reading managers' edits. A record that only one role can
// read is a record the people it describes cannot check, and the point of
// recording agent writes at all is that the person the agent acted for can see
// what it did (D11). Membership is the boundary; role is not. Recorded as D63.
//
// It is a query over the log and nothing more. The v0.1 CSV export (D13) is a
// different shape — several files, flattened for a spreadsheet — and is
// deliberately still deferred; this answers "who changed this and when" in a
// browser, which is the question dogfood will actually ask.
func (s *Service) Audit(ctx context.Context, projectID uuid.UUID, f repo.AuditFilter, p policy.Principal) ([]repo.AuditEntry, error) {
	// Get is the membership gate: a non-member gets not_found, for the same
	// reason every other project-scoped read does.
	if _, err := s.Get(ctx, projectID, p); err != nil {
		return nil, err
	}
	return s.logs.List(ctx, s.db.Pool, projectID, f)
}

// EffectiveRole is the caller's standing on a project, already resolved.
//
// Exposed so the published ownership table (§4.2) is answered with the role the
// write path would use: a manager added to a project as a developer is a
// developer there, and a UI rendered against their global role would offer them
// controls every write then refuses.
func (s *Service) EffectiveRole(ctx context.Context, projectID uuid.UUID, p policy.Principal) (policy.Role, error) {
	sub, err := s.subject(ctx, s.db.Pool, p, projectID)
	if err != nil {
		return "", err
	}
	return sub.EffectiveRole, nil
}
