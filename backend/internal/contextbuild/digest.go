package contextbuild

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

// The digest is two halves that must never touch (CONTRACT §6.3).
//
// **Harness fields are recomputed from the database and cannot drift.** They
// are counts and lists produced by SQL: how many items are open, which are
// blocked, how many deviations are unresolved. Nothing in this file accepts
// them from anywhere else, and there is no setter that takes them as an
// argument — ComputeHarness is the only way to make one.
//
// **Model fields are a hypothesis, not a record.** In LumixAgents Phase 4 a
// model asked to summarise produced `dead_ends` describing role refusals that
// never happened in that run, reconstructed from its own prompt. The fix is not
// a better prompt: it is that the fields a model writes are structurally
// separate from the fields that are true, and the UI must not present them as
// fact.

// HarnessFields is the computed half. Every value here is a query result.
type HarnessFields struct {
	WorkItems       int            `json:"work_items"`
	OpenItems       int            `json:"open_items"`
	ByStatus        map[string]int `json:"by_status"`
	BlockedRefs     []string       `json:"blocked_refs"`
	UnassignedOpen  int            `json:"unassigned_open"`
	OpenDeviations  int            `json:"open_deviations"`
	BySeverity      map[string]int `json:"open_deviations_by_severity"`
	OpenNotes       int            `json:"open_notes"`
	ActiveMilestone string         `json:"active_milestone,omitempty"`
	RecentDecisions []string       `json:"recent_decisions"`
	RejectedRecent  []string       `json:"recently_rejected"`
	Diagrams        int            `json:"diagrams"`
	Entities        int            `json:"entities"`
	IndexedChunks   int            `json:"indexed_chunks"`
	LastActivityAt  *time.Time     `json:"last_activity_at,omitempty"`
}

// ModelFields is the written half. These three names are the whole surface a
// model may write, and adding a fourth is a contract change, not a refactor.
type ModelFields struct {
	CurrentHypothesis string   `json:"current_hypothesis,omitempty"`
	DeadEnds          []string `json:"dead_ends,omitempty"`
	OpenQuestions     []string `json:"open_questions,omitempty"`
}

// Empty reports whether no model has written anything yet.
//
// Used to decide staleness: "stale" on fields that do not exist is a warning
// about nothing, and a UI rendering a staleness badge over an empty panel
// teaches people to ignore the badge.
func (m ModelFields) Empty() bool {
	return m.CurrentHypothesis == "" && len(m.DeadEnds) == 0 && len(m.OpenQuestions) == 0
}

// Digest is the stored row plus its computed staleness.
type Digest struct {
	ProjectID uuid.UUID
	Harness   HarnessFields
	Model     ModelFields

	// Revision is the project's audit-log high-water mark at the moment the
	// model fields were last written.
	//
	// It is what makes staleness *derived* rather than remembered. The
	// alternative — every service that writes anything remembering to set a
	// flag — is a flag that is correct until somebody adds the fourteenth write
	// path. audit_log is append-only and never deleted (§6.5), so its count for
	// a project only goes up, and "has the graph moved since" is a comparison
	// rather than a promise.
	Revision int

	// ModelStale is computed, not trusted: Revision against the current mark.
	ModelStale bool

	UpdatedAt time.Time
}

// HarnessInput is what ComputeHarness is given. Every field is a query result
// from internal/repo; nothing here is a model's opinion.
type HarnessInput struct {
	WorkItems       int
	ByStatus        map[string]int
	BlockedRefs     []string
	UnassignedOpen  int
	Deviations      map[string]int
	OpenNotes       int
	ActiveMilestone string
	RecentDecisions []string
	RejectedRecent  []string
	Diagrams        int
	Entities        int
	IndexedChunks   int
	LastActivityAt  *time.Time
	// ClosedStatuses names the statuses that mean no work remains, supplied by
	// the domain so this file does not restate them.
	ClosedStatuses []string
}

// ComputeHarness derives the harness half. It is the only constructor, and it
// takes no model output.
func ComputeHarness(in HarnessInput) HarnessFields {
	closed := map[string]bool{}
	for _, s := range in.ClosedStatuses {
		closed[s] = true
	}

	out := HarnessFields{
		WorkItems:       in.WorkItems,
		ByStatus:        map[string]int{},
		BlockedRefs:     sortedCopy(in.BlockedRefs),
		UnassignedOpen:  in.UnassignedOpen,
		BySeverity:      map[string]int{},
		OpenNotes:       in.OpenNotes,
		ActiveMilestone: in.ActiveMilestone,
		RecentDecisions: sortedCopy(in.RecentDecisions),
		RejectedRecent:  sortedCopy(in.RejectedRecent),
		Diagrams:        in.Diagrams,
		Entities:        in.Entities,
		IndexedChunks:   in.IndexedChunks,
		LastActivityAt:  in.LastActivityAt,
	}
	for status, n := range in.ByStatus {
		out.ByStatus[status] = n
		if !closed[status] {
			out.OpenItems += n
		}
	}
	for severity, n := range in.Deviations {
		out.BySeverity[severity] = n
		out.OpenDeviations += n
	}
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
