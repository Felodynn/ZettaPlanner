package plan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	pl "github.com/Felodynn/ZettaPlanner/backend/internal/domain/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// CreateWorkItemInput describes a new work item.
type CreateWorkItemInput struct {
	ProjectID   uuid.UUID
	Title       string
	Intent      string
	Acceptance  string
	MilestoneID string
	AssigneeID  string
	Priority    string
	Status      string
	StartDate   string
	DueDate     string
}

// ItemDetail is one work item with everything hanging off it.
//
// Assembled in one transaction so the parts cannot disagree — a subtask list
// read a moment after the item is a list that may describe a different item.
type ItemDetail struct {
	Item      *pl.WorkItem
	Milestone *pl.Milestone
	SubTasks  []*pl.SubTask
	Checklist []*pl.ChecklistItem
	Blocks    []pl.Dependency
	BlockedBy []pl.Dependency
	Relates   []pl.Dependency
	Refs      []pl.EntityRef

	AssigneeName string
}

// ListWorkItems returns a project's work items, filtered.
func (s *Service) ListWorkItems(ctx context.Context, projectID uuid.UUID, f repo.WorkItemFilter, p policy.Principal) ([]*pl.WorkItem, error) {
	if err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	if f.Status != "" {
		if err := pl.ValidateStatus(f.Status); err != nil {
			return nil, err
		}
	}
	return s.items.List(ctx, s.db.Pool, projectID, f)
}

// GetWorkItem returns one work item with its subtasks, checklist, dependencies
// and entity references.
//
// A deleted item is still returned, carrying its deleted_at. Deletion is soft
// (§6.5 constraint 1) and its ref is never reissued, so every worklog and audit
// entry naming ZP-14 still leads somewhere — answering 404 would make the
// history that is demonstrably still in the database unreachable.
func (s *Service) GetWorkItem(ctx context.Context, id uuid.UUID, p policy.Principal) (*ItemDetail, error) {
	var out *ItemDetail
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		item, err := s.items.ByIDAny(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.requireMember(ctx, tx, p, item.ProjectID); err != nil {
			return err
		}
		out, err = s.loadDetail(ctx, tx, item)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ItemByRef resolves a work item by its human-facing ref within a project.
//
// The agent and the CLI both address items the way people do — ZP-14 — and a
// surface that only accepts UUIDs forces every caller to keep a lookup table
// that this one already is.
func (s *Service) ItemByRef(ctx context.Context, projectID uuid.UUID, ref string, p policy.Principal) (*ItemDetail, error) {
	var out *ItemDetail
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.requireMember(ctx, tx, p, projectID); err != nil {
			return err
		}
		item, err := s.items.ByRef(ctx, tx, projectID, strings.ToUpper(strings.TrimSpace(ref)))
		if err != nil {
			return err
		}
		out, err = s.loadDetail(ctx, tx, item)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) loadDetail(ctx context.Context, q store.Querier, item *pl.WorkItem) (*ItemDetail, error) {
	d := &ItemDetail{Item: item}

	if item.MilestoneID != nil {
		m, err := s.milestones.ByID(ctx, q, *item.MilestoneID)
		if err != nil && !isNotFound(err) {
			return nil, err
		}
		d.Milestone = m
	}
	if item.AssigneeID != nil {
		u, err := s.users.ByID(ctx, q, *item.AssigneeID)
		if err != nil && !isNotFound(err) {
			return nil, err
		}
		if u != nil {
			d.AssigneeName = u.DisplayName
		}
	}

	var err error
	if d.SubTasks, err = s.subtasks.ListByItem(ctx, q, item.ID); err != nil {
		return nil, err
	}
	if d.Checklist, err = s.checklists.ListByItem(ctx, q, item.ID); err != nil {
		return nil, err
	}
	if d.Refs, err = s.refs.ListByItem(ctx, q, item.ID); err != nil {
		return nil, err
	}

	outgoing, err := s.deps.Outgoing(ctx, q, item.ID)
	if err != nil {
		return nil, err
	}
	incoming, err := s.deps.Incoming(ctx, q, item.ID)
	if err != nil {
		return nil, err
	}
	for _, dep := range outgoing {
		if dep.Kind == pl.DepBlocks {
			d.Blocks = append(d.Blocks, dep)
		} else {
			d.Relates = append(d.Relates, dep)
		}
	}
	for _, dep := range incoming {
		if dep.Kind == pl.DepBlocks {
			d.BlockedBy = append(d.BlockedBy, dep)
		} else {
			// `relates` is symmetric in meaning even though the row is
			// directed. Showing it from one side only would make the same fact
			// visible on one item and invisible on the other.
			d.Relates = append(d.Relates, dep)
		}
	}
	return d, nil
}

// CreateWorkItem makes a work item. Manager-owned (§4.2).
func (s *Service) CreateWorkItem(ctx context.Context, in CreateWorkItemInput, p policy.Principal) (*pl.WorkItem, error) {
	var created *pl.WorkItem
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		created, err = s.CreateWorkItemInTx(ctx, tx, in, By(p))
		return err
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// CreateWorkItemInTx is CreateWorkItem inside the caller's transaction.
func (s *Service) CreateWorkItemInTx(ctx context.Context, tx pgx.Tx, in CreateWorkItemInput, w Writer) (*pl.WorkItem, error) {
	start, err := pl.ParseDate(in.StartDate)
	if err != nil {
		return nil, err
	}
	due, err := pl.ParseDate(in.DueDate)
	if err != nil {
		return nil, err
	}
	milestoneID, err := parseOptionalUUID("milestone_id", in.MilestoneID)
	if err != nil {
		return nil, err
	}
	assigneeID, err := parseOptionalUUID("assignee_id", in.AssigneeID)
	if err != nil {
		return nil, err
	}

	status := pl.Status(strings.TrimSpace(in.Status))
	if status == "" {
		status = pl.StatusTodo
	}

	subject, err := s.subject(ctx, tx, w, in.ProjectID)
	if err != nil {
		return nil, err
	}
	if d := policy.CheckCreate(policy.KindWorkItem, subject); d != nil {
		return nil, d
	}
	if _, err := s.projects.ByID(ctx, tx, in.ProjectID); err != nil {
		return nil, err
	}
	if err := s.milestoneMustBelong(ctx, tx, in.ProjectID, milestoneID); err != nil {
		return nil, err
	}
	if err := s.assigneeMustBeAMember(ctx, tx, in.ProjectID, assigneeID); err != nil {
		return nil, err
	}

	// Issued inside the transaction. The UPDATE ... RETURNING behind it takes a
	// row lock, which is what serializes two concurrent creates in the same
	// project into two different refs.
	ref, err := s.projects.NextRef(ctx, tx, in.ProjectID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	item := &pl.WorkItem{
		ID:          uuid.Must(uuid.NewV7()),
		ProjectID:   in.ProjectID,
		MilestoneID: milestoneID,
		Ref:         ref,
		Title:       strings.TrimSpace(in.Title),
		Intent:      strings.TrimSpace(in.Intent),
		Acceptance:  strings.TrimSpace(in.Acceptance),
		Status:      status,
		Priority:    pl.Priority(strings.TrimSpace(in.Priority)),
		AssigneeID:  assigneeID,
		StartDate:   start,
		DueDate:     due,
		Provenance:  provenanceFor(w.Principal, "", true),
		ActorID:     w.WritingActorID(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := pl.ValidateWorkItem(item); err != nil {
		return nil, err
	}
	if err := s.items.Create(ctx, tx, item); err != nil {
		return nil, err
	}

	if err := audit.Write(ctx, tx, audit.Entry{
		ProjectID:  &in.ProjectID,
		ActorID:    w.WritingActorID(),
		OnBehalfOf: w.OnBehalfOf(),
		Action:     audit.ActionWorkItemCreated,
		SubjectID:  &item.ID,
		After:      publicWorkItem(item),
	}); err != nil {
		return nil, err
	}
	return item, nil
}

// UpdateWorkItemInput is a patch. See UpdateMilestoneInput on why the optional
// fields carry wire strings.
type UpdateWorkItemInput struct {
	Fields []string

	Title       *string
	Intent      *string
	Acceptance  *string
	Status      *string
	ProgressPct *int
	Priority    *string
	AssigneeID  *string
	MilestoneID *string
	StartDate   *string
	DueDate     *string
}

// UpdateWorkItem patches a work item after checking every field against the
// ownership table.
//
// This is the method the developer/manager split actually runs through: a
// developer patching `status` on an item assigned to them succeeds, the same
// developer patching `due_date` gets a 403 naming the route forward, and a
// manager patching `status` is refused too — ownership is two columns, not two
// tiers, and status is the developer's report of reality.
func (s *Service) UpdateWorkItem(ctx context.Context, id uuid.UUID, in UpdateWorkItemInput, p policy.Principal) (*ItemDetail, error) {
	var out *ItemDetail
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		out, err = s.UpdateWorkItemInTx(ctx, tx, id, in, By(p))
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateWorkItemInTx is UpdateWorkItem inside the caller's transaction.
func (s *Service) UpdateWorkItemInTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, in UpdateWorkItemInput, wr Writer) (*ItemDetail, error) {
	w, err := s.items.ByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	subject, err := s.forItem(ctx, tx, wr, w)
	if err != nil {
		return nil, err
	}
	if d := policy.CheckWrite(policy.KindWorkItem, in.Fields, subject); d != nil {
		return nil, d
	}

	before := publicWorkItem(w)
	beforeStatus, beforeAssignee, beforeMilestone := w.Status, w.AssigneeID, w.MilestoneID

	if in.Title != nil {
		w.Title = strings.TrimSpace(*in.Title)
	}
	if in.Intent != nil {
		w.Intent = strings.TrimSpace(*in.Intent)
	}
	if in.Acceptance != nil {
		w.Acceptance = strings.TrimSpace(*in.Acceptance)
	}
	if in.Status != nil {
		w.Status = pl.Status(strings.TrimSpace(*in.Status))
	}
	if in.ProgressPct != nil {
		w.ProgressPct = *in.ProgressPct
	}
	if in.Priority != nil {
		w.Priority = pl.Priority(strings.TrimSpace(*in.Priority))
	}
	if in.StartDate != nil {
		if w.StartDate, err = pl.ParseDate(*in.StartDate); err != nil {
			return nil, err
		}
	}
	if in.DueDate != nil {
		if w.DueDate, err = pl.ParseDate(*in.DueDate); err != nil {
			return nil, err
		}
	}
	if in.AssigneeID != nil {
		if w.AssigneeID, err = parseOptionalUUID("assignee_id", *in.AssigneeID); err != nil {
			return nil, err
		}
		if err := s.assigneeMustBeAMember(ctx, tx, w.ProjectID, w.AssigneeID); err != nil {
			return nil, err
		}
	}
	if in.MilestoneID != nil {
		if w.MilestoneID, err = parseOptionalUUID("milestone_id", *in.MilestoneID); err != nil {
			return nil, err
		}
		if err := s.milestoneMustBelong(ctx, tx, w.ProjectID, w.MilestoneID); err != nil {
			return nil, err
		}
	}

	w.Provenance = provenanceFor(wr.Principal, w.Provenance, false)
	if err := pl.ValidateWorkItem(w); err != nil {
		return nil, err
	}
	if err := s.items.Update(ctx, tx, w); err != nil {
		return nil, err
	}

	after := publicWorkItem(w)
	entry := func(action audit.Action, b, a any) error {
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &w.ProjectID,
			ActorID:    wr.WritingActorID(),
			OnBehalfOf: wr.OnBehalfOf(),
			Action:     action,
			SubjectID:  &w.ID,
			Before:     b,
			After:      a,
		})
	}

	// Three transitions and one generic edit, each written only when it actually
	// happened. A single PATCH can produce all four; they share the request id,
	// which is what groups them back into one operation without flattening four
	// distinct facts into one row.
	if changed(beforeStatus, w.Status) {
		if err := entry(audit.ActionWorkItemStatusChanged,
			map[string]any{"status": string(beforeStatus)},
			map[string]any{"status": string(w.Status)}); err != nil {
			return nil, err
		}
	}
	if !sameUUID(beforeAssignee, w.AssigneeID) {
		if err := entry(audit.ActionWorkItemAssigned,
			map[string]any{"assignee_id": uuidString(beforeAssignee)},
			map[string]any{"assignee_id": uuidString(w.AssigneeID)}); err != nil {
			return nil, err
		}
	}
	if !sameUUID(beforeMilestone, w.MilestoneID) {
		if err := entry(audit.ActionWorkItemMoved,
			map[string]any{"milestone_id": uuidString(beforeMilestone)},
			map[string]any{"milestone_id": uuidString(w.MilestoneID)}); err != nil {
			return nil, err
		}
	}
	if changedBeyondStatus(before, after, "assignee_id", "milestone_id") {
		if err := entry(audit.ActionWorkItemUpdated, before, after); err != nil {
			return nil, err
		}
	}

	return s.loadDetail(ctx, tx, w)
}

// DeleteWorkItem soft-deletes a work item.
func (s *Service) DeleteWorkItem(ctx context.Context, id uuid.UUID, p policy.Principal) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		return s.DeleteWorkItemInTx(ctx, tx, id, By(p))
	})
}

// DeleteWorkItemInTx is DeleteWorkItem inside the caller's transaction.
func (s *Service) DeleteWorkItemInTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, wr Writer) error {
	w, err := s.items.ByID(ctx, tx, id)
	if err != nil {
		return err
	}
	subject, err := s.forItem(ctx, tx, wr, w)
	if err != nil {
		return err
	}
	if d := policy.CheckDelete(policy.KindWorkItem, subject); d != nil {
		return d
	}
	if err := s.items.Delete(ctx, tx, id); err != nil {
		return err
	}
	return audit.Write(ctx, tx, audit.Entry{
		ProjectID:  &w.ProjectID,
		ActorID:    wr.WritingActorID(),
		OnBehalfOf: wr.OnBehalfOf(),
		Action:     audit.ActionWorkItemDeleted,
		SubjectID:  &w.ID,
		Before:     publicWorkItem(w),
	})
}

// milestoneMustBelong rejects moving an item into a milestone from another
// project. The foreign key permits it; the plan does not.
func (s *Service) milestoneMustBelong(ctx context.Context, q store.Querier, projectID uuid.UUID, milestoneID *uuid.UUID) error {
	if milestoneID == nil {
		return nil
	}
	m, err := s.milestones.ByID(ctx, q, *milestoneID)
	if err != nil {
		return err
	}
	if m.ProjectID != projectID {
		return fmt.Errorf("%w: milestone %q belongs to a different project", domain.ErrInvalid, m.Name)
	}
	return nil
}

func publicWorkItem(w *pl.WorkItem) map[string]any {
	return map[string]any{
		"id":                  w.ID,
		"ref":                 w.Ref,
		"title":               w.Title,
		"intent":              w.Intent,
		"acceptance_criteria": w.Acceptance,
		"status":              string(w.Status),
		"progress_pct":        w.ProgressPct,
		"priority":            string(w.Priority),
		"assignee_id":         uuidString(w.AssigneeID),
		"milestone_id":        uuidString(w.MilestoneID),
		"start_date":          pl.FormatDate(w.StartDate),
		"due_date":            pl.FormatDate(w.DueDate),
	}
}

// uuidString renders an optional id for an audit snapshot.
//
// A string, not the pointer. Two snapshots are compared field by field to
// decide whether anything moved, and comparing pointers compares addresses —
// which differ every time and would report a change on every request.
func uuidString(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// parseOptionalUUID reads an id from a wire string, where "" means "clear it".
//
// The empty string carries that meaning because a JSON null cannot: both an
// absent field and an explicit null decode into a nil pointer, so a patch has
// no other way to say "remove the assignee" as distinct from "leave it alone".
func parseOptionalUUID(field, raw string) (*uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is %q, which is not an id. Send an id, or \"\" to clear it",
			domain.ErrInvalid, field, raw)
	}
	return &id, nil
}
