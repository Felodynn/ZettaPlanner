package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// DigestBody is a project's digest (CONTRACT §6.3).
//
// The two halves are separate objects on the wire as well as separate columns
// in the database, and the model half carries `stale` next to it. **The UI must
// not present model fields as fact.** They are a hypothesis: in LumixAgents
// Phase 4 a model asked to summarise produced `dead_ends` describing events
// that never happened, reconstructed from its own prompt.
type DigestBody struct {
	Harness DigestHarness `json:"harness" doc:"Recomputed from the database on every read. Cannot drift"`
	Model   DigestModel   `json:"model" doc:"Written by a model. A hypothesis, not a record — never present these as fact"`
	// ModelStale is derived, not stored: the digest carries the project's
	// audit-log high-water mark from when the model fields were written, and
	// the log is append-only, so this is a comparison rather than a flag
	// somebody has to remember to set.
	ModelStale bool      `json:"model_stale" doc:"True when anything has been recorded in this project since the model fields were written"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// DigestHarness is the computed half. Every value is a query result.
type DigestHarness struct {
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
	IndexedChunks   int            `json:"indexed_chunks" doc:"How much spill is retrievable. Zero means layer 3 will find nothing"`
	LastActivityAt  *time.Time     `json:"last_activity_at,omitempty"`
}

// DigestModel is the written half. These three names are the whole surface a
// model may write.
type DigestModel struct {
	CurrentHypothesis string   `json:"current_hypothesis,omitempty"`
	DeadEnds          []string `json:"dead_ends,omitempty"`
	OpenQuestions     []string `json:"open_questions,omitempty"`
}

type digestOutput struct{ Body DigestBody }

func (s *Server) registerDigest() {
	huma.Register(s.API, huma.Operation{
		OperationID: "get-digest",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/digest",
		Summary:     "The project digest: computed facts and the model's hypothesis, kept apart",
		Description: "The harness half is recomputed from the database on every read and cannot drift. " +
			"The model half is a hypothesis and is marked stale the moment anything is recorded in the " +
			"project after it was written. Never render the two the same way.",
		Tags: []string{"context"},
	}, func(ctx context.Context, in *struct {
		PID uuid.UUID `path:"pid"`
	}) (*digestOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		d, err := s.deps.Context.Digest(ctx, in.PID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}

		out := &digestOutput{Body: DigestBody{
			Harness: DigestHarness{
				WorkItems:       d.Harness.WorkItems,
				OpenItems:       d.Harness.OpenItems,
				ByStatus:        orEmptyCounts(d.Harness.ByStatus),
				BlockedRefs:     orEmptyStrings(d.Harness.BlockedRefs),
				UnassignedOpen:  d.Harness.UnassignedOpen,
				OpenDeviations:  d.Harness.OpenDeviations,
				BySeverity:      orEmptyCounts(d.Harness.BySeverity),
				OpenNotes:       d.Harness.OpenNotes,
				ActiveMilestone: d.Harness.ActiveMilestone,
				RecentDecisions: orEmptyStrings(d.Harness.RecentDecisions),
				RejectedRecent:  orEmptyStrings(d.Harness.RejectedRecent),
				Diagrams:        d.Harness.Diagrams,
				Entities:        d.Harness.Entities,
				IndexedChunks:   d.Harness.IndexedChunks,
				LastActivityAt:  d.Harness.LastActivityAt,
			},
			Model: DigestModel{
				CurrentHypothesis: d.Model.CurrentHypothesis,
				DeadEnds:          d.Model.DeadEnds,
				OpenQuestions:     d.Model.OpenQuestions,
			},
			ModelStale: d.ModelStale,
			UpdatedAt:  d.UpdatedAt,
		}}
		return out, nil
	})
}

func orEmptyCounts(m map[string]int) map[string]int {
	if m == nil {
		return map[string]int{}
	}
	return m
}

func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
