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
)

// CreateSubTaskInput describes a new subtask.
type CreateSubTaskInput struct {
	WorkItemID uuid.UUID
	Title      string
	Status     string
	AssigneeID string
	Seq        *int
}

// CreateSubTask adds a subtask under a work item.
//
// Dev-owned, scoped to the assignee of the **parent item** (§4.2: "within own
// item"). A subtask carries its own assignee_id, and being named there does not
// grant write access — the table is explicit that the scope is the item. That
// is a sharp edge worth watching in dogfood: it means handing someone a subtask
// without reassigning the item leaves them unable to tick it.
func (s *Service) CreateSubTask(ctx context.Context, in CreateSubTaskInput, p policy.Principal) (*pl.SubTask, error) {
	assigneeID, err := parseOptionalUUID("assignee_id", in.AssigneeID)
	if err != nil {
		return nil, err
	}
	status := pl.Status(strings.TrimSpace(in.Status))
	if status == "" {
		status = pl.StatusTodo
	}

	var created *pl.SubTask
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		item, err := s.items.ByID(ctx, tx, in.WorkItemID)
		if err != nil {
			return err
		}
		subject, err := s.forItem(ctx, tx, p, item)
		if err != nil {
			return err
		}
		if d := policy.CheckCreate(policy.KindSubTask, subject); d != nil {
			return d
		}
		if err := s.assigneeMustBeAMember(ctx, tx, item.ProjectID, assigneeID); err != nil {
			return err
		}

		seq := 0
		if in.Seq != nil {
			seq = *in.Seq
		} else if seq, err = s.subtasks.NextSeq(ctx, tx, item.ID); err != nil {
			return err
		}

		now := time.Now()
		st := &pl.SubTask{
			ID:         uuid.Must(uuid.NewV7()),
			WorkItemID: item.ID,
			Title:      strings.TrimSpace(in.Title),
			Status:     status,
			Seq:        seq,
			AssigneeID: assigneeID,
			Provenance: provenanceFor(p, "", true),
			ActorID:    p.WritingActorID(),
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := pl.ValidateSubTask(st); err != nil {
			return err
		}
		if err := s.subtasks.Create(ctx, tx, st); err != nil {
			return err
		}

		created = st
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &item.ProjectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionSubTaskCreated,
			SubjectID:  &st.ID,
			After:      publicSubTask(st),
		})
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateSubTaskInput is a patch.
type UpdateSubTaskInput struct {
	Fields []string

	Title      *string
	Status     *string
	AssigneeID *string
	Seq        *int
}

// UpdateSubTask patches a subtask.
func (s *Service) UpdateSubTask(ctx context.Context, id uuid.UUID, in UpdateSubTaskInput, p policy.Principal) (*pl.SubTask, error) {
	var updated *pl.SubTask

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		st, err := s.subtasks.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		item, err := s.items.ByID(ctx, tx, st.WorkItemID)
		if err != nil {
			return err
		}
		subject, err := s.forItem(ctx, tx, p, item)
		if err != nil {
			return err
		}
		if d := policy.CheckWrite(policy.KindSubTask, in.Fields, subject); d != nil {
			return d
		}

		before := publicSubTask(st)
		beforeStatus := st.Status

		if in.Title != nil {
			st.Title = strings.TrimSpace(*in.Title)
		}
		if in.Status != nil {
			st.Status = pl.Status(strings.TrimSpace(*in.Status))
		}
		if in.Seq != nil {
			st.Seq = *in.Seq
		}
		if in.AssigneeID != nil {
			if st.AssigneeID, err = parseOptionalUUID("assignee_id", *in.AssigneeID); err != nil {
				return err
			}
			if err := s.assigneeMustBeAMember(ctx, tx, item.ProjectID, st.AssigneeID); err != nil {
				return err
			}
		}

		st.Provenance = provenanceFor(p, st.Provenance, false)
		if err := pl.ValidateSubTask(st); err != nil {
			return err
		}
		if err := s.subtasks.Update(ctx, tx, st); err != nil {
			return err
		}

		updated = st
		after := publicSubTask(st)

		if changed(beforeStatus, st.Status) {
			if err := audit.Write(ctx, tx, audit.Entry{
				ProjectID:  &item.ProjectID,
				ActorID:    p.WritingActorID(),
				OnBehalfOf: p.OnBehalfOf(),
				Action:     audit.ActionSubTaskStatusChanged,
				SubjectID:  &st.ID,
				Before:     map[string]any{"status": string(beforeStatus)},
				After:      map[string]any{"status": string(st.Status)},
			}); err != nil {
				return err
			}
		}
		if !changedBeyondStatus(before, after) {
			return nil
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &item.ProjectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionSubTaskUpdated,
			SubjectID:  &st.ID,
			Before:     before,
			After:      after,
		})
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteSubTask soft-deletes a subtask.
func (s *Service) DeleteSubTask(ctx context.Context, id uuid.UUID, p policy.Principal) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		st, err := s.subtasks.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		item, err := s.items.ByID(ctx, tx, st.WorkItemID)
		if err != nil {
			return err
		}
		subject, err := s.forItem(ctx, tx, p, item)
		if err != nil {
			return err
		}
		if d := policy.CheckDelete(policy.KindSubTask, subject); d != nil {
			return d
		}
		if err := s.subtasks.Delete(ctx, tx, id); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &item.ProjectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionSubTaskDeleted,
			SubjectID:  &st.ID,
			Before:     publicSubTask(st),
		})
	})
}

func publicSubTask(st *pl.SubTask) map[string]any {
	return map[string]any{
		"id":           st.ID,
		"work_item_id": st.WorkItemID,
		"title":        st.Title,
		"status":       string(st.Status),
		"seq":          st.Seq,
		"assignee_id":  uuidString(st.AssigneeID),
	}
}
