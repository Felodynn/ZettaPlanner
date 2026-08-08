// Package thread holds the conversation's pure types: threads, messages, and
// agent turns.
//
// **Chat is transcript, never memory** (D8). Nothing in this package is fed to
// a model as history; a turn receives assembled state from
// internal/contextbuild and the current prompt, and the transcript exists so
// people can read what was said. That distinction is the reason a long
// conversation does not slowly poison every subsequent answer.
//
// Threads are **globally visible to every project member** (§3.6), including
// managers reading developers' turns. Scope controls context assembly and UI
// placement, never visibility.
package thread

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
)

// ── scope ────────────────────────────────────────────────────────────────────

// ScopeKind is what a thread is about.
type ScopeKind string

const (
	ScopeProject  ScopeKind = "project"
	ScopeWorkItem ScopeKind = "work_item"
	ScopeDiagram  ScopeKind = "diagram"
)

var scopeMeaning = map[ScopeKind]string{
	ScopeProject:  "the whole project; there is exactly one of these per project",
	ScopeWorkItem: "one work item, so assembly starts from it without being told",
	ScopeDiagram:  "one diagram, for conversations about the architecture it draws",
}

// ScopeKinds returns every scope.
func ScopeKinds() []ScopeKind { return []ScopeKind{ScopeProject, ScopeWorkItem, ScopeDiagram} }

// Valid reports whether k is a known scope.
func (k ScopeKind) Valid() bool { _, ok := scopeMeaning[k]; return ok }

// ScopeMeaning returns the one-line gloss.
func ScopeMeaning(k ScopeKind) string { return scopeMeaning[k] }

func (k ScopeKind) String() string { return string(k) }

// ValidateScopeKind returns nil or a teaching error.
func ValidateScopeKind(k ScopeKind) error {
	if k.Valid() {
		return nil
	}
	return &domain.VocabularyError{
		Field: "thread scope",
		Value: string(k),
		Valid: domain.Glossed(ScopeKinds(), ScopeMeaning),
		Hint: "Scope decides what context assembly starts from and where the UI puts the thread. " +
			"It never decides who can read it — every thread is readable by every project member (§3.6).",
	}
}

// Thread is one conversation.
type Thread struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	ScopeKind ScopeKind
	ScopeID   *uuid.UUID
	CreatedAt time.Time

	// ScopeLabel is filled on read: the item's ref, the diagram's name. A
	// thread listed as a UUID is a thread nobody can choose between.
	ScopeLabel string
	// MessageCount and LastMessageAt are computed, never stored.
	MessageCount  int
	LastMessageAt *time.Time
}

// ── messages ─────────────────────────────────────────────────────────────────

// Role is who produced a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

// Roles returns every role.
func Roles() []Role { return []Role{RoleUser, RoleAssistant, RoleTool, RoleSystem} }

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleUser, RoleAssistant, RoleTool, RoleSystem:
		return true
	}
	return false
}

func (r Role) String() string { return string(r) }

// ToolCall is one call the model asked for, as persisted.
type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
	// Outcome is what happened: ok, denied, error, unknown. Persisted with the
	// call rather than only in the tool result, so a transcript reader sees
	// which calls were refused without joining two rows.
	Outcome string `json:"outcome,omitempty"`
}

// Message is one entry in a thread. Never deleted; correct by adding (§6.5).
type Message struct {
	ID        uuid.UUID
	ThreadID  uuid.UUID
	ActorID   uuid.UUID
	Role      Role
	Content   string
	ToolCalls []ToolCall
	TurnID    *uuid.UUID
	Seq       int64
	CreatedAt time.Time

	// ActorName is filled on read.
	ActorName string
}

// ── turns ────────────────────────────────────────────────────────────────────

// TurnStatus is a turn's lifecycle state.
type TurnStatus string

const (
	TurnQueued    TurnStatus = "queued"
	TurnRunning   TurnStatus = "running"
	TurnComplete  TurnStatus = "complete"
	TurnFailed    TurnStatus = "failed"
	TurnAbandoned TurnStatus = "abandoned"
	TurnCancelled TurnStatus = "cancelled"
)

var turnStatusMeaning = map[TurnStatus]string{
	TurnQueued:    "waiting for the project's running turn to finish (§5.3)",
	TurnRunning:   "in flight; exactly one per project at a time",
	TurnComplete:  "ended having done something — a write, a proposal, or a handoff",
	TurnFailed:    "ended without doing anything useful; finish_reason says why",
	TurnAbandoned: "its process died and the lease lapsed; partial output kept, nothing applied",
	TurnCancelled: "a human stopped it",
}

// TurnStatuses returns every status.
func TurnStatuses() []TurnStatus {
	return []TurnStatus{TurnQueued, TurnRunning, TurnComplete, TurnFailed, TurnAbandoned, TurnCancelled}
}

// Valid reports whether s is a known status.
func (s TurnStatus) Valid() bool { _, ok := turnStatusMeaning[s]; return ok }

// TurnStatusMeaning returns the one-line gloss.
func TurnStatusMeaning(s TurnStatus) string { return turnStatusMeaning[s] }

// Terminal reports whether the turn is over.
func (s TurnStatus) Terminal() bool {
	switch s {
	case TurnComplete, TurnFailed, TurnAbandoned, TurnCancelled:
		return true
	}
	return false
}

func (s TurnStatus) String() string { return string(s) }

// FinishReason says why a turn ended, which the status cannot.
//
// A turn that ran to a `finish` call and one that hit MaxTurns are both over,
// and only one of them answered the question.
type FinishReason string

const (
	FinishFinished FinishReason = "finished"
	// FinishNoOutput is the one worth naming. A turn that declines in prose and
	// writes nothing looks identical to a completed one from the outside — the
	// gap LumixAgents Phase 6 identified and left open.
	FinishNoOutput  FinishReason = "no_output"
	FinishMaxTurns  FinishReason = "max_turns"
	FinishMaxTokens FinishReason = "max_tokens"
	FinishMaxWall   FinishReason = "max_wall_clock"
	FinishError     FinishReason = "error"
	FinishCancelled FinishReason = "cancelled"
	FinishAbandoned FinishReason = "abandoned"
)

var finishMeaning = map[FinishReason]string{
	FinishFinished:  "the agent called `finish` having done something",
	FinishNoOutput:  "the turn ended with no write, no proposal and no handoff — not a success",
	FinishMaxTurns:  "the loop hit its step ceiling",
	FinishMaxTokens: "the turn hit its token ceiling",
	FinishMaxWall:   "the turn hit its wall-clock ceiling",
	FinishError:     "a non-retryable model or harness failure",
	FinishCancelled: "a human stopped it",
	FinishAbandoned: "its process died and the lease lapsed",
}

// FinishReasons returns every reason.
func FinishReasons() []FinishReason {
	return []FinishReason{
		FinishFinished, FinishNoOutput, FinishMaxTurns, FinishMaxTokens,
		FinishMaxWall, FinishError, FinishCancelled, FinishAbandoned,
	}
}

// FinishMeaning returns the one-line gloss.
func FinishMeaning(r FinishReason) string { return finishMeaning[r] }

// Succeeded reports whether the turn did something. Only `finished` counts:
// every other reason is a turn that ended without answering.
func (r FinishReason) Succeeded() bool { return r == FinishFinished }

func (r FinishReason) String() string { return string(r) }

// Turn is one agent run.
type Turn struct {
	ID          uuid.UUID
	ProjectID   uuid.UUID
	ThreadID    uuid.UUID
	RequestedBy uuid.UUID
	MessageID   *uuid.UUID
	Status      TurnStatus
	Finish      FinishReason

	Model       string
	TokensIn    int
	TokensOut   int
	Steps       int
	Retries     int
	WallMS      int
	ErrorClass  string
	ErrorDetail string

	QueuedAt    time.Time
	HeartbeatAt *time.Time
	StartedAt   *time.Time
	EndedAt     *time.Time

	// QueuePosition is computed on read: 0 for a running turn, 1 for the next
	// in line. §5.3 requires a queued turn to be *shown as* queued, which means
	// the position has to be an answer rather than an inference.
	QueuePosition int
	// RequestedByName is filled on read.
	RequestedByName string
}

// Live reports whether the turn still holds the project's slot.
func (t *Turn) Live() bool { return t.Status == TurnQueued || t.Status == TurnRunning }

// ── errors ───────────────────────────────────────────────────────────────────

// TurnInProgressError is a second turn arriving while one runs.
//
// Not an error the caller has to avoid — posting a message while a turn runs is
// normal, and the message lands. This is only raised where a caller demanded
// immediate execution.
type TurnInProgressError struct {
	Position  int
	HolderFor string
}

func (e *TurnInProgressError) Error() string {
	return fmt.Sprintf(
		"another turn is running on this project and yours is queued at position %d. "+
			"Turns run one at a time per project: two agents writing to the same plan produce interleaved "+
			"changes with no coherent audit story (§5.3)", e.Position)
}

// Is makes it answer to ErrConflict for callers that only need the category.
func (e *TurnInProgressError) Is(target error) bool { return target == domain.ErrConflict }
