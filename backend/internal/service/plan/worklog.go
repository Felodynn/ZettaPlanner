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
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
)

// AddWorklog appends an entry to the log.
//
// Anyone on the project may write one, manager and developer alike (D42). A
// manager who cannot record what happened is a manager who stops recording it,
// and the worklog is where the retrospective's evidence actually comes from.
//
// There is no update and no delete: a worklog is corrected by adding another.
func (s *Service) AddWorklog(ctx context.Context, itemID *uuid.UUID, projectID uuid.UUID, kind, body string, p policy.Principal) (*pl.Worklog, error) {
	var created *pl.Worklog

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		project := projectID
		if itemID != nil {
			item, err := s.items.ByID(ctx, tx, *itemID)
			if err != nil {
				return err
			}
			project = item.ProjectID
		}

		subject, err := s.subject(ctx, tx, p, project)
		if err != nil {
			return err
		}
		if d := policy.CheckCreate(policy.KindWorklog, subject); d != nil {
			return d
		}

		w := &pl.Worklog{
			ID:         uuid.Must(uuid.NewV7()),
			ProjectID:  project,
			WorkItemID: itemID,
			ActorID:    p.WritingActorID(),
			Kind:       pl.WorklogKind(strings.TrimSpace(kind)),
			Body:       strings.TrimSpace(body),
			CreatedAt:  time.Now(),
		}
		if w.Kind == "" {
			w.Kind = pl.WorklogProgress
		}
		if err := pl.ValidateWorklog(w); err != nil {
			return err
		}
		if err := s.worklogs.Create(ctx, tx, w); err != nil {
			return err
		}

		created = w
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &project,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionWorklogAdded,
			SubjectID:  &w.ID,
			After: map[string]any{
				"id":           w.ID,
				"work_item_id": uuidString(w.WorkItemID),
				"kind":         string(w.Kind),
				"body":         w.Body,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	// Re-read so the caller gets the item ref and the actor's display name,
	// which the insert does not know.
	return s.worklogs.ByID(ctx, s.db.Pool, created.ID)
}

// ListWorklogs returns a project's worklogs, newest first.
func (s *Service) ListWorklogs(ctx context.Context, projectID uuid.UUID, f repo.WorklogFilter, p policy.Principal) ([]*pl.Worklog, error) {
	if err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	if f.Kind != "" {
		if err := pl.ValidateWorklogKind(f.Kind); err != nil {
			return nil, err
		}
	}
	return s.worklogs.List(ctx, s.db.Pool, projectID, f)
}
