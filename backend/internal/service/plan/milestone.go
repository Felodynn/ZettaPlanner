package plan

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// CreateMilestoneInput describes a new milestone.
type CreateMilestoneInput struct {
	ProjectID uuid.UUID
	Name      string
	Intent    string
	StartDate string
	DueDate   string
	Status    string
	Seq       *int
}

// ListMilestones returns a project's milestones.
func (s *Service) ListMilestones(ctx context.Context, projectID uuid.UUID, p policy.Principal) ([]*pl.Milestone, error) {
	if err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.milestones.ListByProject(ctx, s.db.Pool, projectID)
}

// GetMilestone returns one milestone.
func (s *Service) GetMilestone(ctx context.Context, id uuid.UUID, p policy.Principal) (*pl.Milestone, error) {
	m, err := s.milestones.ByID(ctx, s.db.Pool, id)
	if err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, s.db.Pool, p, m.ProjectID); err != nil {
		return nil, err
	}
	return m, nil
}

// CreateMilestone makes a milestone. Manager-owned (§4.2).
func (s *Service) CreateMilestone(ctx context.Context, in CreateMilestoneInput, p policy.Principal) (*pl.Milestone, error) {
	var created *pl.Milestone
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		created, err = s.CreateMilestoneInTx(ctx, tx, in, By(p))
		return err
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// CreateMilestoneInTx is CreateMilestone inside the caller's transaction.
func (s *Service) CreateMilestoneInTx(ctx context.Context, tx pgx.Tx, in CreateMilestoneInput, w Writer) (*pl.Milestone, error) {
	start, err := pl.ParseDate(in.StartDate)
	if err != nil {
		return nil, err
	}
	due, err := pl.ParseDate(in.DueDate)
	if err != nil {
		return nil, err
	}

	status := pl.MilestoneStatus(strings.TrimSpace(in.Status))
	if status == "" {
		status = pl.MilestonePlanned
	}

	subject, err := s.subject(ctx, tx, w, in.ProjectID)
	if err != nil {
		return nil, err
	}
	if d := policy.CheckCreate(policy.KindMilestone, subject); d != nil {
		return nil, d
	}
	if _, err := s.projects.ByID(ctx, tx, in.ProjectID); err != nil {
		return nil, err
	}

	seq := 0
	if in.Seq != nil {
		seq = *in.Seq
	} else if seq, err = s.milestones.NextSeq(ctx, tx, in.ProjectID); err != nil {
		return nil, err
	}

	now := time.Now()
	m := &pl.Milestone{
		ID:         uuid.Must(uuid.NewV7()),
		ProjectID:  in.ProjectID,
		Name:       strings.TrimSpace(in.Name),
		Intent:     strings.TrimSpace(in.Intent),
		Seq:        seq,
		StartDate:  start,
		DueDate:    due,
		Status:     status,
		Provenance: provenanceFor(w.Principal, "", true),
		ActorID:    w.WritingActorID(),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := pl.ValidateMilestone(m); err != nil {
		return nil, err
	}
	if err := s.milestones.Create(ctx, tx, m); err != nil {
		return nil, err
	}

	if err := audit.Write(ctx, tx, audit.Entry{
		ProjectID:  &in.ProjectID,
		ActorID:    w.WritingActorID(),
		OnBehalfOf: w.OnBehalfOf(),
		Action:     audit.ActionMilestoneCreated,
		SubjectID:  &m.ID,
		After:      publicMilestone(m),
	}); err != nil {
		return nil, err
	}
	return m, nil
}

// UpdateMilestoneInput is a patch.
//
// Fields is the set of wire names the request actually set, derived by
// policy.Fields. Nothing below this line inspects which particular field it is
// when deciding whether the write is allowed.
//
// The date and status fields carry wire strings rather than parsed values,
// which is what lets "" mean "clear this" — the one thing a JSON null cannot
// say through a Go pointer, because both absent and null decode to nil.
type UpdateMilestoneInput struct {
	Fields []string

	Name      *string
	Intent    *string
	StartDate *string
	DueDate   *string
	Status    *string
	Seq       *int
}

// UpdateMilestone patches a milestone after checking every field.
func (s *Service) UpdateMilestone(ctx context.Context, id uuid.UUID, in UpdateMilestoneInput, p policy.Principal) (*pl.Milestone, error) {
	var updated *pl.Milestone
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		updated, err = s.UpdateMilestoneInTx(ctx, tx, id, in, By(p))
		return err
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// UpdateMilestoneInTx is UpdateMilestone inside the caller's transaction.
func (s *Service) UpdateMilestoneInTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, in UpdateMilestoneInput, w Writer) (*pl.Milestone, error) {
	m, err := s.milestones.ByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	subject, err := s.subject(ctx, tx, w, m.ProjectID)
	if err != nil {
		return nil, err
	}
	if d := policy.CheckWrite(policy.KindMilestone, in.Fields, subject); d != nil {
		return nil, d
	}

	before := publicMilestone(m)
	beforeStatus := m.Status

	if in.Name != nil {
		m.Name = strings.TrimSpace(*in.Name)
	}
	if in.Intent != nil {
		m.Intent = strings.TrimSpace(*in.Intent)
	}
	if in.Seq != nil {
		m.Seq = *in.Seq
	}
	if in.StartDate != nil {
		if m.StartDate, err = pl.ParseDate(*in.StartDate); err != nil {
			return nil, err
		}
	}
	if in.DueDate != nil {
		if m.DueDate, err = pl.ParseDate(*in.DueDate); err != nil {
			return nil, err
		}
	}
	if in.Status != nil {
		m.Status = pl.MilestoneStatus(strings.TrimSpace(*in.Status))
	}

	m.Provenance = provenanceFor(w.Principal, m.Provenance, false)
	if err := pl.ValidateMilestone(m); err != nil {
		return nil, err
	}
	if err := s.milestones.Update(ctx, tx, m); err != nil {
		return nil, err
	}

	after := publicMilestone(m)

	// The status move is its own entry, not a generic update. Both are written
	// when a patch does both: they are two facts, and the shared request_id is
	// what puts them back together.
	if changed(beforeStatus, m.Status) {
		if err := audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &m.ProjectID,
			ActorID:    w.WritingActorID(),
			OnBehalfOf: w.OnBehalfOf(),
			Action:     audit.ActionMilestoneStatusChanged,
			SubjectID:  &m.ID,
			Before:     map[string]any{"status": string(beforeStatus)},
			After:      map[string]any{"status": string(m.Status)},
		}); err != nil {
			return nil, err
		}
	}
	if !changedBeyondStatus(before, after) {
		return m, nil
	}
	if err := audit.Write(ctx, tx, audit.Entry{
		ProjectID:  &m.ProjectID,
		ActorID:    w.WritingActorID(),
		OnBehalfOf: w.OnBehalfOf(),
		Action:     audit.ActionMilestoneUpdated,
		SubjectID:  &m.ID,
		Before:     before,
		After:      after,
	}); err != nil {
		return nil, err
	}
	return m, nil
}

// DeleteMilestone soft-deletes a milestone and detaches its work items.
func (s *Service) DeleteMilestone(ctx context.Context, id uuid.UUID, p policy.Principal) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		return s.DeleteMilestoneInTx(ctx, tx, id, By(p))
	})
}

// DeleteMilestoneInTx is DeleteMilestone inside the caller's transaction.
func (s *Service) DeleteMilestoneInTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, w Writer) error {
	m, err := s.milestones.ByID(ctx, tx, id)
	if err != nil {
		return err
	}
	subject, err := s.subject(ctx, tx, w, m.ProjectID)
	if err != nil {
		return err
	}
	if d := policy.CheckDelete(policy.KindMilestone, subject); d != nil {
		return d
	}

	// The items survive: deleting a grouping must not delete the work. They lose
	// the pointer, because a milestone_id resolving to nothing is a filter every
	// join downstream has to remember.
	detached, err := s.milestones.DetachItems(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := s.milestones.Delete(ctx, tx, id); err != nil {
		return err
	}

	before := publicMilestone(m)
	before["work_items_detached"] = detached
	return audit.Write(ctx, tx, audit.Entry{
		ProjectID:  &m.ProjectID,
		ActorID:    w.WritingActorID(),
		OnBehalfOf: w.OnBehalfOf(),
		Action:     audit.ActionMilestoneDeleted,
		SubjectID:  &m.ID,
		Before:     before,
	})
}

// ItemCounts returns, per milestone, how many live work items sit under it and
// how many are still open. Read-only, computed, never stored.
func (s *Service) ItemCounts(ctx context.Context, q store.Querier, projectID uuid.UUID) (map[uuid.UUID][2]int, error) {
	const sql = `
		SELECT milestone_id, count(*), count(*) FILTER (WHERE status <> ALL($2::text[]))
		  FROM work_items
		 WHERE project_id = $1 AND deleted_at IS NULL AND milestone_id IS NOT NULL
		 GROUP BY milestone_id`
	rows, err := q.Query(ctx, sql, projectID, pl.ClosedStatuses())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID][2]int{}
	for rows.Next() {
		var (
			id          uuid.UUID
			total, open int
		)
		if err := rows.Scan(&id, &total, &open); err != nil {
			return nil, err
		}
		out[id] = [2]int{total, open}
	}
	return out, rows.Err()
}

func publicMilestone(m *pl.Milestone) map[string]any {
	return map[string]any{
		"id":         m.ID,
		"name":       m.Name,
		"intent":     m.Intent,
		"seq":        m.Seq,
		"start_date": pl.FormatDate(m.StartDate),
		"due_date":   pl.FormatDate(m.DueDate),
		"status":     string(m.Status),
	}
}

// changedBeyondStatus reports whether two snapshots differ in anything other
// than the fields already recorded as transitions.
//
// It is what stops a status-only PATCH also writing a `*.updated` entry whose
// before and after are identical. An audit row saying nothing changed is worse
// than no row: it puts an edit in the history that never happened, and the
// export cannot tell it from one that did.
func changedBeyondStatus(before, after map[string]any, alsoRecorded ...string) bool {
	skip := map[string]bool{"status": true}
	for _, k := range alsoRecorded {
		skip[k] = true
	}
	for k, b := range before {
		if skip[k] {
			continue
		}
		if a, ok := after[k]; !ok || a != b {
			return true
		}
	}
	return false
}
