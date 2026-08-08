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

// AddChecklistItem adds a box to a work item's checklist.
func (s *Service) AddChecklistItem(ctx context.Context, itemID uuid.UUID, text string, seq *int, p policy.Principal) (*pl.ChecklistItem, error) {
	var created *pl.ChecklistItem

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		item, err := s.items.ByID(ctx, tx, itemID)
		if err != nil {
			return err
		}
		subject, err := s.forItem(ctx, tx, By(p), item)
		if err != nil {
			return err
		}
		if d := policy.CheckCreate(policy.KindChecklistItem, subject); d != nil {
			return d
		}

		position := 0
		if seq != nil {
			position = *seq
		} else if position, err = s.checklists.NextSeq(ctx, tx, itemID); err != nil {
			return err
		}

		c := &pl.ChecklistItem{
			ID:         uuid.Must(uuid.NewV7()),
			WorkItemID: itemID,
			Text:       strings.TrimSpace(text),
			Seq:        position,
		}
		if err := pl.ValidateChecklistItem(c); err != nil {
			return err
		}
		if err := s.checklists.Create(ctx, tx, c); err != nil {
			return err
		}

		created = c
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &item.ProjectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionChecklistCreated,
			SubjectID:  &c.ID,
			After:      publicChecklistItem(c),
		})
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateChecklistInput is a patch.
//
// No Seq. CONTRACT §4.2 gives a checklist item exactly two writable fields,
// text and checked, and a third one here would be a field written with no owner
// checked. Reordering a checklist is a v0.1 change to the table, not something
// to slip in through a patch body.
type UpdateChecklistInput struct {
	Fields []string

	Text    *string
	Checked *bool
}

// UpdateChecklistItem patches a checklist item.
//
// A tick and an un-tick are both recorded as `checklist.checked`, with before
// and after carrying which way it went. Recording only the tick hides the
// clear, and a box ticked and then cleared is a signal — usually that the work
// was not as done as somebody thought.
func (s *Service) UpdateChecklistItem(ctx context.Context, id uuid.UUID, in UpdateChecklistInput, p policy.Principal) (*pl.ChecklistItem, error) {
	var updated *pl.ChecklistItem

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		c, err := s.checklists.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		item, err := s.items.ByID(ctx, tx, c.WorkItemID)
		if err != nil {
			return err
		}
		subject, err := s.forItem(ctx, tx, By(p), item)
		if err != nil {
			return err
		}
		if d := policy.CheckWrite(policy.KindChecklistItem, in.Fields, subject); d != nil {
			return d
		}

		before := publicChecklistItem(c)
		beforeChecked := c.Checked

		if in.Text != nil {
			c.Text = strings.TrimSpace(*in.Text)
		}
		if in.Checked != nil && *in.Checked != c.Checked {
			c.Checked = *in.Checked
			if c.Checked {
				now := time.Now()
				actor := p.WritingActorID()
				c.CheckedBy, c.CheckedAt = &actor, &now
			}
			// On an un-tick CheckedBy and CheckedAt stay as they were: "who
			// ticked this, and when" is a fact that survives somebody clearing
			// the box, and the clear itself is in the audit log.
		}

		if err := pl.ValidateChecklistItem(c); err != nil {
			return err
		}
		if err := s.checklists.Update(ctx, tx, c); err != nil {
			return err
		}

		updated = c
		after := publicChecklistItem(c)

		if beforeChecked != c.Checked {
			if err := audit.Write(ctx, tx, audit.Entry{
				ProjectID:  &item.ProjectID,
				ActorID:    p.WritingActorID(),
				OnBehalfOf: p.OnBehalfOf(),
				Action:     audit.ActionChecklistChecked,
				SubjectID:  &c.ID,
				Before:     map[string]any{"checked": beforeChecked, "text": before["text"]},
				After:      map[string]any{"checked": c.Checked, "text": after["text"]},
			}); err != nil {
				return err
			}
		}
		if !changedBeyondStatus(before, after, "checked") {
			return nil
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &item.ProjectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionChecklistUpdated,
			SubjectID:  &c.ID,
			Before:     before,
			After:      after,
		})
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteChecklistItem soft-deletes a checklist item.
func (s *Service) DeleteChecklistItem(ctx context.Context, id uuid.UUID, p policy.Principal) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		c, err := s.checklists.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		item, err := s.items.ByID(ctx, tx, c.WorkItemID)
		if err != nil {
			return err
		}
		subject, err := s.forItem(ctx, tx, By(p), item)
		if err != nil {
			return err
		}
		if d := policy.CheckDelete(policy.KindChecklistItem, subject); d != nil {
			return d
		}
		if err := s.checklists.Delete(ctx, tx, id); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &item.ProjectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionChecklistDeleted,
			SubjectID:  &c.ID,
			Before:     publicChecklistItem(c),
		})
	})
}

func publicChecklistItem(c *pl.ChecklistItem) map[string]any {
	return map[string]any{
		"id":           c.ID,
		"work_item_id": c.WorkItemID,
		"text":         c.Text,
		"checked":      c.Checked,
		"seq":          c.Seq,
	}
}
