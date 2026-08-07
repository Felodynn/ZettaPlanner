package repo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Sessions is the sessions aggregate.
//
// Sessions are revoked, never deleted, so "this session ended at 14:02" stays
// answerable next to the audit log.
type Sessions struct{}

// Create inserts a session.
func (Sessions) Create(ctx context.Context, q store.Querier, s *domain.Session) error {
	const sql = `
		INSERT INTO sessions (id, token_hash, user_id, created_at, last_seen_at, expires_at, user_agent, ip)
		VALUES ($1, $2, $3, $4, $4, $5, $6, $7)`
	_, err := q.Exec(ctx, sql,
		s.ID, s.TokenHash, s.UserID, s.CreatedAt, s.ExpiresAt,
		nullIfEmpty(s.UserAgent), nullIfEmpty(s.IP))
	return err
}

// Principal resolves a session token hash straight to the acting principal.
//
// One query rather than session-then-user, because it runs on every
// authenticated request. It returns nothing for a revoked, expired, deleted or
// deactivated account, so the middleware has no chance to forget one of those
// conditions.
func (Sessions) Principal(ctx context.Context, q store.Querier, tokenHash []byte, now time.Time) (*policy.Principal, uuid.UUID, error) {
	const sql = `
		SELECT s.id, u.id, a.id, u.email, u.display_name, u.role, u.is_active
		  FROM sessions s
		  JOIN users  u ON u.id = s.user_id
		  JOIN actors a ON a.user_id = u.id AND a.kind = 'human'
		 WHERE s.token_hash = $1
		   AND s.revoked_at IS NULL
		   AND s.expires_at > $2
		   AND u.deleted_at IS NULL
		   AND u.is_active`

	var (
		sessionID uuid.UUID
		p         policy.Principal
		role      string
	)
	err := q.QueryRow(ctx, sql, tokenHash, now).
		Scan(&sessionID, &p.UserID, &p.ActorID, &p.Email, &p.DisplayName, &role, &p.IsActive)
	if err != nil {
		return nil, uuid.Nil, notFound(err, "session")
	}
	p.GlobalRole = policy.Role(role)
	return &p, sessionID, nil
}

// Touch records that a session was used. Best-effort: a failure here must never
// fail the request it was observing.
func (Sessions) Touch(ctx context.Context, q store.Querier, id uuid.UUID, at time.Time) error {
	const sql = `UPDATE sessions SET last_seen_at = $2 WHERE id = $1`
	_, err := q.Exec(ctx, sql, id, at)
	return err
}

// Revoke ends one session.
func (Sessions) Revoke(ctx context.Context, q store.Querier, id uuid.UUID) error {
	const sql = `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`
	_, err := q.Exec(ctx, sql, id)
	return err
}

// RevokeAllForUser ends every live session a user holds.
//
// Called on login, which is the rotation the contract asks for, and on
// deactivation, where a still-live session would outlast the account.
func (Sessions) RevokeAllForUser(ctx context.Context, q store.Querier, userID uuid.UUID) (int64, error) {
	const sql = `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`
	tag, err := q.Exec(ctx, sql, userID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
