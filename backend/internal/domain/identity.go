// Package domain holds pure types and their invariants. No I/O, no SQL, no
// HTTP. A type here can be constructed and checked in a test with no
// infrastructure at all.
package domain

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
)

// Sentinel errors. The HTTP layer maps these onto contract codes in exactly one
// place; nothing below httpapi knows what a status code is.
var (
	ErrNotFound      = errors.New("not found")
	ErrConflict      = errors.New("conflict")
	ErrInvalid       = errors.New("invalid")
	ErrCredentials   = errors.New("invalid credentials")
	ErrInactive      = errors.New("account is not active")
	ErrNoSession     = errors.New("no session")
	ErrSessionExpiry = errors.New("session expired")
)

// ActorKind distinguishes the three sorts of principal (CONTRACT §4.1).
type ActorKind string

const (
	ActorHuman  ActorKind = "human"
	ActorAgent  ActorKind = "agent"
	ActorSystem ActorKind = "system"
)

// Actor is a principal that can appear on an audit row.
//
// Agents get an actor row at creation, not on first use, so an audit entry can
// never reference a principal that does not exist.
type Actor struct {
	ID        uuid.UUID
	Kind      ActorKind
	UserID    *uuid.UUID
	AgentName string
	ModelRef  string
	CreatedAt time.Time
}

// User is a local account.
type User struct {
	ID           uuid.UUID
	Email        string
	DisplayName  string
	PasswordHash string
	Role         policy.Role
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}

// NormaliseEmail lower-cases and trims an address so that two spellings of the
// same mailbox cannot become two accounts.
func NormaliseEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateUser checks the invariants that must hold before a row is written.
func ValidateUser(email, displayName string, role policy.Role) error {
	if _, err := mail.ParseAddress(email); err != nil {
		return fmt.Errorf("%w: %q is not an email address", ErrInvalid, email)
	}
	if strings.TrimSpace(displayName) == "" {
		return fmt.Errorf("%w: display_name is empty", ErrInvalid)
	}
	if !role.Valid() {
		return fmt.Errorf("%w: %q is not a role; the roles are admin, manager, developer", ErrInvalid, role)
	}
	return nil
}

// ProjectStatus values.
const (
	ProjectActive   = "active"
	ProjectArchived = "archived"
)

// Project is one planned body of work. Multiple projects per deployment;
// multiple customers, never (D3).
type Project struct {
	ID          uuid.UUID
	Key         string
	Name        string
	Description string
	Status      string
	Settings    map[string]any
	GitEnabled  bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

// ValidateProject checks a project's invariants.
//
// The key is the human-facing prefix that work item refs are built from
// (ZP-14), so it is constrained to something that reads well in a ref and can
// never need escaping.
func ValidateProject(key, name, status string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalid)
	}
	if key == "" {
		return fmt.Errorf("%w: key is empty; it is the prefix of every work item ref, such as ZP in ZP-14", ErrInvalid)
	}
	if len(key) > 10 {
		return fmt.Errorf("%w: key %q is longer than 10 characters; it prefixes every work item ref", ErrInvalid, key)
	}
	for _, r := range key {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			return fmt.Errorf("%w: key %q must be upper-case letters and digits only, so that refs like %s-14 never need escaping",
				ErrInvalid, key, key)
		}
	}
	switch status {
	case ProjectActive, ProjectArchived:
	default:
		return fmt.Errorf("%w: status %q is not one of %s, %s", ErrInvalid, status, ProjectActive, ProjectArchived)
	}
	return nil
}

// ProjectMember binds a user to a project, optionally overriding their role.
type ProjectMember struct {
	ProjectID uuid.UUID
	UserID    uuid.UUID
	Role      policy.Role
}

// Session is a logged-in browser session.
//
// TokenHash is the SHA-256 of the cookie value. The token itself exists only
// in the response that issued it and in the client's cookie jar.
type Session struct {
	ID         uuid.UUID
	TokenHash  []byte
	UserID     uuid.UUID
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	UserAgent  string
	IP         string
}

// Live reports whether the session may still authenticate a request.
func (s *Session) Live(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}
