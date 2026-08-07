// Package auth owns local accounts and sessions.
//
// It owns its transactions: one logical operation is one transaction and one
// audit entry, written inside it. A login that produced a session row but no
// audit entry is not a state this package can reach.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// DefaultSessionTTL is how long a session lives without being renewed.
const DefaultSessionTTL = 14 * 24 * time.Hour

// Service is the auth application service.
type Service struct {
	db  *store.DB
	log *slog.Logger
	ttl time.Duration

	users    repo.Users
	actors   repo.Actors
	sessions repo.Sessions
	members  repo.Members
}

// New builds the service.
func New(db *store.DB, log *slog.Logger, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, log: log, ttl: ttl}
}

// ── login ────────────────────────────────────────────────────────────────────

// LoginInput is a login attempt.
type LoginInput struct {
	Email    string
	Password string

	// CurrentSessionID, when set, is revoked as part of issuing the new
	// session. That is the rotation: a session identifier never survives a
	// login.
	CurrentSessionID *uuid.UUID

	UserAgent string
	IP        string
}

// LoginResult carries the token the caller must set as a cookie. The token
// exists here and in the client's cookie jar, and nowhere else — the database
// holds only its SHA-256.
type LoginResult struct {
	Token     string
	ExpiresAt time.Time
	Principal policy.Principal
}

// Login verifies a password and issues a session.
func (s *Service) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	email := domain.NormaliseEmail(in.Email)

	user, err := s.users.ByEmail(ctx, s.db.Pool, email)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	if user == nil {
		// Verify against a fixed hash anyway. Returning early here would make
		// an unknown address measurably faster than a known one, which
		// enumerates accounts from the outside.
		_, _ = VerifyPassword(in.Password, dummyHash)
		s.log.WarnContext(ctx, "login rejected: no such account", "email", email, "ip", in.IP)
		return nil, domain.ErrCredentials
	}

	ok, err := VerifyPassword(in.Password, user.PasswordHash)
	if err != nil {
		// A corrupt stored hash fails this login and is loud about it, rather
		// than being reported to the user as a wrong password they cannot fix.
		s.log.ErrorContext(ctx, "stored password hash is unreadable", "user_id", user.ID, "error", err)
		return nil, domain.ErrCredentials
	}
	if !ok || !user.IsActive {
		if err := s.recordRejection(ctx, user); err != nil {
			return nil, err
		}
		if !user.IsActive {
			return nil, domain.ErrInactive
		}
		return nil, domain.ErrCredentials
	}

	token, hash, err := newSessionToken()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	session := &domain.Session{
		ID:         uuid.Must(uuid.NewV7()),
		TokenHash:  hash,
		UserID:     user.ID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.ttl),
		UserAgent:  in.UserAgent,
		IP:         in.IP,
	}

	var principal policy.Principal
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		actor, err := s.actors.ForUser(ctx, tx, user.ID)
		if err != nil {
			return fmt.Errorf("user %s has no actor row; every principal gets one at creation: %w", user.ID, err)
		}

		// Rotation. The identifier the request arrived with does not survive a
		// login, so a fixed session cannot be promoted to an authenticated one.
		if in.CurrentSessionID != nil {
			if err := s.sessions.Revoke(ctx, tx, *in.CurrentSessionID); err != nil {
				return err
			}
		}
		if err := s.sessions.Create(ctx, tx, session); err != nil {
			return err
		}

		principal = policy.Principal{
			UserID:      user.ID,
			ActorID:     actor.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			GlobalRole:  user.Role,
			IsActive:    user.IsActive,
		}

		return audit.Write(ctx, tx, audit.Entry{
			ActorID:   actor.ID,
			Action:    audit.ActionSessionStarted,
			SubjectID: &session.ID,
			After: map[string]any{
				"user_id":    user.ID,
				"expires_at": session.ExpiresAt,
				"ip":         in.IP,
				"rotated":    in.CurrentSessionID != nil,
			},
		})
	})
	if err != nil {
		return nil, err
	}

	return &LoginResult{Token: token, ExpiresAt: session.ExpiresAt, Principal: principal}, nil
}

// recordRejection audits a failed attempt against an account that exists.
//
// Only for existing accounts. An unknown address is logged but not audited: the
// log's retention is never (§6.5 constraint 5), and an unauthenticated caller
// able to append a row per request is an unauthenticated caller able to grow a
// table that is never pruned. Attempts against real accounts are bounded by the
// user count and are the ones worth having.
func (s *Service) recordRejection(ctx context.Context, user *domain.User) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		actor, err := s.actors.ForUser(ctx, tx, user.ID)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorID:   actor.ID,
			Action:    audit.ActionSessionRejected,
			SubjectID: &user.ID,
			After:     map[string]any{"reason": rejectionReason(user)},
		})
	})
}

func rejectionReason(u *domain.User) string {
	if !u.IsActive {
		return "account is not active"
	}
	return "password did not match"
}

// ── session lifecycle ────────────────────────────────────────────────────────

// Authenticate resolves a raw session token to a principal.
//
// Returns domain.ErrNoSession for anything unusable — revoked, expired, deleted
// account, deactivated account — so a caller cannot distinguish between them and
// learn something from the difference.
func (s *Service) Authenticate(ctx context.Context, token string) (*policy.Principal, uuid.UUID, error) {
	if token == "" {
		return nil, uuid.Nil, domain.ErrNoSession
	}
	hash := hashToken(token)

	principal, sessionID, err := s.sessions.Principal(ctx, s.db.Pool, hash, time.Now())
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, uuid.Nil, domain.ErrNoSession
		}
		return nil, uuid.Nil, err
	}
	return principal, sessionID, nil
}

// Touch records that a session was used. Best-effort by design: failing a
// request because a bookkeeping update failed trades a working product for a
// tidier timestamp.
func (s *Service) Touch(ctx context.Context, sessionID uuid.UUID) {
	if err := s.sessions.Touch(ctx, s.db.Pool, sessionID, time.Now()); err != nil {
		s.log.WarnContext(ctx, "could not update session last_seen_at", "session_id", sessionID, "error", err)
	}
}

// Logout revokes the caller's session.
func (s *Service) Logout(ctx context.Context, sessionID uuid.UUID, p policy.Principal) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.sessions.Revoke(ctx, tx, sessionID); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionSessionEnded,
			SubjectID:  &sessionID,
			After:      map[string]any{"reason": "logout"},
		})
	})
}

// ── me ───────────────────────────────────────────────────────────────────────

// Me is the authenticated caller plus their effective role on every project
// they can see (CONTRACT §7.1, `auth/me`).
type Me struct {
	Principal policy.Principal
	Projects  []ProjectRole
}

// ProjectRole is one project and the caller's resolved role on it.
type ProjectRole struct {
	ProjectID   uuid.UUID
	ProjectKey  string
	ProjectName string

	// Effective is what actually governs writes: the project override when
	// there is one, otherwise the global role — and always admin for an admin.
	Effective policy.Role
	Override  *policy.Role
}

// Me returns the caller and their per-project effective roles.
func (s *Service) Me(ctx context.Context, p policy.Principal) (*Me, error) {
	projects, err := (repo.Projects{}).ListVisible(ctx, s.db.Pool, p.UserID, p.GlobalRole == policy.RoleAdmin)
	if err != nil {
		return nil, err
	}
	overrides, err := s.members.ProjectRoles(ctx, s.db.Pool, p.UserID)
	if err != nil {
		return nil, err
	}

	out := &Me{Principal: p}
	for _, proj := range projects {
		pr := ProjectRole{ProjectID: proj.ID, ProjectKey: proj.Key, ProjectName: proj.Name}
		if role, ok := overrides[proj.ID]; ok {
			r := role
			pr.Override = &r
		}
		pr.Effective = policy.Effective(p.GlobalRole, pr.Override)
		out.Projects = append(out.Projects, pr)
	}
	return out, nil
}

// Subject assembles the policy subject for a write in one project.
//
// The relationship flags an object-level check needs (assignee, author,
// reporter) are added by the service that loaded the target row; this fills in
// only what is knowable from membership.
func (s *Service) Subject(ctx context.Context, q store.Querier, p policy.Principal, projectID uuid.UUID) (policy.Subject, error) {
	override, err := s.members.RoleFor(ctx, q, projectID, p.UserID)
	if err != nil {
		return policy.Subject{}, err
	}
	return policy.Subject{
		Principal:     p,
		ProjectID:     projectID,
		EffectiveRole: policy.Effective(p.GlobalRole, override),
		IsMember:      override != nil,
	}, nil
}

// ── tokens ───────────────────────────────────────────────────────────────────

// newSessionToken returns a fresh token and the hash to store.
func newSessionToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generating a session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
