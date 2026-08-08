// Package policy owns roles, principals, and the field ownership table.
//
// D7: field ownership is mechanical, computed from role and assignment. An
// agent judging whether its own change needs approval is not a control, and
// neither is a handler remembering to check. The write path checks the field.
//
// Nothing in this package does I/O. It is given the facts — effective role,
// whether the caller is the assignee — and answers whether the write is
// allowed. That makes the whole table testable without a database and makes
// the answer identical for a human, an agent, and the CLI.
package policy

import (
	"context"
	"fmt"
	"sort"

	"github.com/google/uuid"
)

// Role is a principal's authority level (CONTRACT §4.1).
type Role string

const (
	RoleAdmin     Role = "admin"
	RoleManager   Role = "manager"
	RoleDeveloper Role = "developer"
)

// Roles returns every role, most authority first.
func Roles() []Role { return []Role{RoleAdmin, RoleManager, RoleDeveloper} }

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleAdmin, RoleManager, RoleDeveloper:
		return true
	}
	return false
}

// rank orders roles by authority. Used only for "at least" comparisons; it is
// deliberately not how field ownership is decided, because ownership is not a
// ladder — a manager does not own `progress_pct` merely by outranking a
// developer.
func (r Role) rank() int {
	switch r {
	case RoleAdmin:
		return 3
	case RoleManager:
		return 2
	case RoleDeveloper:
		return 1
	}
	return 0
}

// AtLeast reports whether r carries at least the authority of other.
func (r Role) AtLeast(other Role) bool { return r.rank() >= other.rank() }

func (r Role) String() string { return string(r) }

// Effective resolves the role a principal actually has on a project.
//
// The per-project override wins when present, including when it grants *less*
// than the global role: a manager added to a project as a developer is a
// developer there. `admin` is the exception — deployment-level authority is not
// something a project membership can take away (CONTRACT §4.1).
func Effective(global Role, override *Role) Role {
	if global == RoleAdmin {
		return RoleAdmin
	}
	if override != nil && override.Valid() {
		return *override
	}
	return global
}

// Principal is who is acting. Every write carries one.
type Principal struct {
	UserID      uuid.UUID
	ActorID     uuid.UUID
	Email       string
	DisplayName string
	GlobalRole  Role
	IsActive    bool

	// AgentActorID is set when an agent is acting on this user's instruction.
	// The agent has the user's effective permissions and no more (D11), and the
	// audit entry records both.
	AgentActorID *uuid.UUID
}

// IsAgent reports whether an agent is acting for this principal.
func (p Principal) IsAgent() bool { return p.AgentActorID != nil }

// WritingActorID is the actor an audit row should name: the agent when one is
// acting, otherwise the human.
func (p Principal) WritingActorID() uuid.UUID {
	if p.AgentActorID != nil {
		return *p.AgentActorID
	}
	return p.ActorID
}

// OnBehalfOf is the instructing user for an agent write, and nil otherwise.
func (p Principal) OnBehalfOf() *uuid.UUID {
	if p.AgentActorID == nil {
		return nil
	}
	id := p.UserID
	return &id
}

// Subject is a principal together with everything the ownership table needs to
// decide one specific write.
//
// The relationship flags are supplied by the service that loaded the target
// row. They are never inferred here, because inferring them would mean this
// package doing I/O and the check drifting from the row it is about.
type Subject struct {
	Principal

	// ProjectID is the project the write lands in. uuid.Nil for
	// deployment-level operations.
	ProjectID uuid.UUID

	// EffectiveRole is Effective(global, project override), already resolved.
	EffectiveRole Role

	// IsMember reports whether the principal belongs to the project at all.
	IsMember bool

	// IsAssignee: the principal is the assignee of the work item this write
	// concerns — the item itself, or the item owning the subtask or checklist
	// item being written.
	IsAssignee bool

	// IsAuthor: the principal wrote the worklog being edited.
	IsAuthor bool

	// IsReporter: the principal raised the deviation being edited.
	IsReporter bool

	// ApprovedProposal is the proposal this write is applying, set only after
	// the approval gate has run.
	//
	// §5.1 gives `admin` and `manager` approval authority over anything, while
	// §4.2 makes dev-owned fields a ceiling — a manager may not patch `status`,
	// because it is the developer's report of reality and overwriting it
	// destroys the signal rather than correcting it. Both are true at once, and
	// a proposal is what reconciles them: the governed route is deliberate,
	// carries a rationale, names an approver, and leaves an audit row saying who
	// raised it and who decided. That is a different act from a silent patch.
	//
	// **Only the governance service may set this**, and only after deciding who
	// was allowed to approve. Setting it anywhere else turns the ownership table
	// into a suggestion.
	ApprovedProposal *uuid.UUID
}

// UnderApprovedProposal reports whether this write is applying a decided
// proposal.
func (s Subject) UnderApprovedProposal() bool { return s.ApprovedProposal != nil }

// ── principal in context ─────────────────────────────────────────────────────

type ctxKey int

const principalKey ctxKey = iota

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFrom returns the principal carried by ctx.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// MustPrincipal returns the principal or panics. Only for code paths already
// behind authentication, where its absence is a wiring bug rather than a
// request the caller can fix.
func MustPrincipal(ctx context.Context) Principal {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		panic("policy: no principal in context; the handler is not behind the session middleware")
	}
	return p
}

// ParseRole validates a role string, listing the valid values on failure — the
// error is read by a model as well as a human.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if r.Valid() {
		return r, nil
	}
	names := make([]string, 0, len(Roles()))
	for _, v := range Roles() {
		names = append(names, string(v))
	}
	sort.Strings(names)
	return "", fmt.Errorf("%q is not a role; the roles are %v", s, names)
}
