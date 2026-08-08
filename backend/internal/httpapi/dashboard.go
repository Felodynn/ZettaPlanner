package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
)

// ── dashboard ────────────────────────────────────────────────────────────────

// DashboardBody is a project's rollup, computed on read.
//
// Every number here is a query result and none of it is stored, for the same
// reason warnings are not (§3.5): a saved rollup is stale the moment a date
// moves and reads identically whether it is true now or was true in April.
type DashboardBody struct {
	Totals      DashboardTotals   `json:"totals"`
	Milestones  []MilestoneLoad   `json:"milestones"`
	Assignees   []AssigneeLoad    `json:"assignees" doc:"Open work per person. The entry with no user_id is the unassigned pile"`
	Upcoming    []DatedItemView   `json:"upcoming" doc:"Open items already overdue, plus those due within horizon_days"`
	Blocked     []BlockedItemView `json:"blocked"`
	Warnings    []WarningSummary  `json:"warnings" doc:"The same computation as GET /warnings, from the same query"`
	HorizonDays int               `json:"horizon_days"`
}

// DashboardTotals is the headline counts.
type DashboardTotals struct {
	Items          int            `json:"work_items"`
	Open           int            `json:"open_items"`
	ByStatus       map[string]int `json:"by_status"`
	Untriaged      int            `json:"untriaged_open" doc:"Open items with no priority. An unset priority is not normal (§3.5a) — it means nobody has looked"`
	OpenDeviations int            `json:"open_deviations"`
	BySeverity     map[string]int `json:"open_deviations_by_severity"`
	PendingReview  int            `json:"pending_proposals"`
}

// MilestoneLoad is a milestone with what is under it.
type MilestoneLoad struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status" enum:"planned,active,done,dropped"`
	Seq       int       `json:"seq"`
	StartDate string    `json:"start_date,omitempty"`
	DueDate   string    `json:"due_date,omitempty"`
	Total     int       `json:"item_count"`
	Open      int       `json:"open_count"`
	Done      int       `json:"done_count"`
	Blocked   int       `json:"blocked_count"`
	Overdue   int       `json:"overdue_count"`
}

// AssigneeLoad is one person's open work.
type AssigneeLoad struct {
	UserID     string `json:"user_id,omitempty" doc:"Absent on the unassigned row"`
	Name       string `json:"name,omitempty"`
	Open       int    `json:"open"`
	InProgress int    `json:"in_progress"`
	Blocked    int    `json:"blocked"`
	Overdue    int    `json:"overdue"`
}

// DatedItemView is one open item with a date worth looking at.
type DatedItemView struct {
	ID       uuid.UUID `json:"id"`
	Ref      string    `json:"ref"`
	Title    string    `json:"title"`
	Status   string    `json:"status"`
	Priority string    `json:"priority,omitempty"`
	DueDate  string    `json:"due_date"`
	Assignee string    `json:"assignee,omitempty"`
	Overdue  bool      `json:"overdue"`
}

// BlockedItemView is an item in `blocked` and what is holding it.
type BlockedItemView struct {
	ID    uuid.UUID `json:"id"`
	Ref   string    `json:"ref"`
	Title string    `json:"title"`
	// BlockedBy is empty when somebody set the status and never recorded a
	// dependency — a real answer, and the interesting one, because nothing can
	// tell them when it clears.
	BlockedBy []string `json:"blocked_by"`
}

func (s *Server) registerDashboard() {
	huma.Register(s.API, huma.Operation{
		OperationID: "get-dashboard",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/dashboard",
		Summary:     "A project's rollup: load, dates, blockers, warnings",
		Description: "One call rather than six, because a view assembled from six calls renders six moments " +
			"and the one that matters — is anything overdue — arrives last. Nothing here is stored or " +
			"cached; every figure is an aggregate over live rows.",
		Tags: []string{"plan"},
	}, func(ctx context.Context, in *struct {
		PID     uuid.UUID `path:"pid"`
		Horizon int       `query:"horizon_days" default:"14" minimum:"1" maximum:"180" doc:"How far ahead to look for due dates. Overdue items are always included"`
	}) (*struct{ Body DashboardBody }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		d, err := s.deps.Plan.Dashboard(ctx, in.PID, in.Horizon, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &struct{ Body DashboardBody }{Body: dashboardBody(d)}, nil
	})
}

func dashboardBody(d *plansvc.Dashboard) DashboardBody {
	out := DashboardBody{
		Totals: DashboardTotals{
			Items:          d.Totals.Items,
			Open:           d.Totals.Open,
			ByStatus:       orEmptyCounts(d.Totals.ByStatus),
			Untriaged:      d.Totals.Untriaged,
			OpenDeviations: d.Totals.OpenDeviations,
			BySeverity:     orEmptyCounts(d.Totals.BySeverity),
			PendingReview:  d.Totals.PendingReview,
		},
		Milestones:  []MilestoneLoad{},
		Assignees:   []AssigneeLoad{},
		Upcoming:    []DatedItemView{},
		Blocked:     []BlockedItemView{},
		Warnings:    warningSummaries(d.Warnings),
		HorizonDays: d.HorizonDays,
	}

	for _, m := range d.Milestones {
		out.Milestones = append(out.Milestones, MilestoneLoad{
			ID: m.Milestone.ID, Name: m.Milestone.Name, Status: string(m.Milestone.Status),
			Seq:       m.Milestone.Seq,
			StartDate: pl.FormatDate(m.Milestone.StartDate),
			DueDate:   pl.FormatDate(m.Milestone.DueDate),
			Total:     m.Stat.Total, Open: m.Stat.Open, Done: m.Stat.Done,
			Blocked: m.Stat.Blocked, Overdue: m.Stat.Overdue,
		})
	}
	for _, a := range d.Assignees {
		row := AssigneeLoad{
			Name: a.Name, Open: a.Open, InProgress: a.InProgress,
			Blocked: a.Blocked, Overdue: a.Overdue,
		}
		if a.UserID != nil {
			row.UserID = a.UserID.String()
		}
		out.Assignees = append(out.Assignees, row)
	}
	for _, it := range d.Upcoming {
		out.Upcoming = append(out.Upcoming, DatedItemView{
			ID: it.ID, Ref: it.Ref, Title: it.Title,
			Status: string(it.Status), Priority: string(it.Priority),
			DueDate: pl.FormatDate(it.DueDate), Assignee: it.Assignee, Overdue: it.Overdue,
		})
	}
	for _, b := range d.Blocked {
		refs := b.BlockedBy
		if refs == nil {
			refs = []string{}
		}
		out.Blocked = append(out.Blocked, BlockedItemView{
			ID: b.ID, Ref: b.Ref, Title: b.Title, BlockedBy: refs,
		})
	}
	return out
}

func warningSummaries(list []pl.Warning) []WarningSummary {
	out := make([]WarningSummary, 0, len(list))
	for _, w := range list {
		out = append(out, warningSummary(w))
	}
	return out
}

// auditViews renders the log for the wire.
func auditViews(entries []repo.AuditEntry) []AuditEntryView {
	out := make([]AuditEntryView, 0, len(entries))
	for _, e := range entries {
		v := AuditEntryView{
			ID: e.ID, At: e.At, Action: e.Action,
			IsTransition: audit.Action(e.Action).IsTransition(),
			SubjectKind:  e.SubjectKind,
			ActorID:      e.Actor.ID, ActorKind: e.Actor.Kind,
			ActorName: e.Actor.Name, ActorModel: e.Actor.ModelRef,
			OnBehalfName: e.OnBehalfName,
			RequestID:    e.RequestID,
			Before:       e.Before, After: e.After,
		}
		if e.SubjectID != nil {
			v.SubjectID = e.SubjectID.String()
		}
		if e.OnBehalfOf != nil {
			v.OnBehalfOf = e.OnBehalfOf.String()
		}
		out = append(out, v)
	}
	return out
}

// ── audit ────────────────────────────────────────────────────────────────────

// AuditEntryView is one record.
//
// Both the actor and the instructing user are named, because an agent write is
// two facts — what ran and who asked (D11) — and a view that shows only one of
// them cannot answer either question.
type AuditEntryView struct {
	ID           uuid.UUID       `json:"id"`
	At           time.Time       `json:"at"`
	Action       string          `json:"action"`
	IsTransition bool            `json:"is_transition" doc:"True when this records a state change with a meaningful before and after, rather than an edit"`
	SubjectKind  string          `json:"subject_kind"`
	SubjectID    string          `json:"subject_id,omitempty"`
	ActorID      uuid.UUID       `json:"actor_id"`
	ActorKind    string          `json:"actor_kind" enum:"human,agent,system"`
	ActorName    string          `json:"actor_name"`
	ActorModel   string          `json:"actor_model,omitempty" doc:"Which model an agent was running. The same agent on a different model is a different reader of the same prompt"`
	OnBehalfOf   string          `json:"on_behalf_of,omitempty" doc:"The instructing user when an agent acted"`
	OnBehalfName string          `json:"on_behalf_of_name,omitempty"`
	RequestID    string          `json:"request_id" doc:"Groups one logical operation. Fourteen entries sharing one is one proposal applied, not an afternoon of edits"`
	Before       json.RawMessage `json:"before,omitempty"`
	After        json.RawMessage `json:"after,omitempty"`
}

func (s *Server) registerAudit() {
	huma.Register(s.API, huma.Operation{
		OperationID: "get-project-audit",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/audit",
		Summary:     "Who changed what, including every agent write",
		Description: "Readable by any member of the project, not only by managers — the same call §3.6 makes " +
			"about the conversation. A record the people it describes cannot read is not a control.\n\n" +
			"State transitions are their own actions (`work_item.status_changed`, not a generic update), so " +
			"\"how long did this sit in blocked\" is a query over this rather than a diff of two JSONB blobs.",
		Tags: []string{"audit"},
	}, func(ctx context.Context, in *struct {
		PID       uuid.UUID `path:"pid"`
		Subject   string    `query:"subject" doc:"Subject kind, such as work_item or diagram"`
		SubjectID string    `query:"subject_id" doc:"One thing's whole history"`
		Actor     string    `query:"actor" doc:"Actor id. Note this is an actor, not a user: an agent has its own"`
		Action    string    `query:"action"`
		Since     string    `query:"since" doc:"RFC3339"`
		Until     string    `query:"until" doc:"RFC3339"`
		Limit     int       `query:"limit" default:"100" minimum:"1" maximum:"500"`
	}) (*struct {
		Body struct {
			Entries []AuditEntryView `json:"entries"`
		}
	}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}

		f := repo.AuditFilter{SubjectKind: in.Subject, Action: in.Action, Limit: in.Limit}
		if in.SubjectID != "" {
			id, err := uuid.Parse(in.SubjectID)
			if err != nil {
				return nil, huma.Error400BadRequest("subject_id is not a uuid")
			}
			f.SubjectID = &id
		}
		if in.Actor != "" {
			id, err := uuid.Parse(in.Actor)
			if err != nil {
				return nil, huma.Error400BadRequest("actor is an actor id, which is a uuid. " +
					"An agent has its own actor row, separate from the user it acted for")
			}
			f.ActorID = &id
		}
		if f.Since, err = parseInstant(in.Since, "since"); err != nil {
			return nil, err
		}
		if f.Until, err = parseInstant(in.Until, "until"); err != nil {
			return nil, err
		}

		entries, err := s.deps.Projects.Audit(ctx, in.PID, f, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &struct {
			Body struct {
				Entries []AuditEntryView `json:"entries"`
			}
		}{}
		out.Body.Entries = auditViews(entries)
		return out, nil
	})
}

func parseInstant(s, field string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, huma.Error400BadRequest(field + " is not an RFC3339 timestamp, for example 2026-08-08T09:30:00Z")
	}
	return &t, nil
}
