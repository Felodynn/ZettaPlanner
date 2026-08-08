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

// CreateDeviationInput describes a divergence between the plan and reality.
type CreateDeviationInput struct {
	ProjectID  uuid.UUID
	WorkItemID string
	Summary    string
	Severity   string
	// Body, when set, files a worklog alongside the deviation and links the
	// two. A deviation is a claim; the worklog is the account of it, and the
	// pair is much more use later than either alone.
	Body string
}

// RaiseDeviation records a deviation, optionally with the worklog describing it.
func (s *Service) RaiseDeviation(ctx context.Context, in CreateDeviationInput, p policy.Principal) (*pl.Deviation, error) {
	itemID, err := parseOptionalUUID("work_item_id", in.WorkItemID)
	if err != nil {
		return nil, err
	}
	severity := pl.Severity(strings.TrimSpace(in.Severity))
	if severity == "" {
		severity = pl.SeverityMedium
	}

	var created *pl.Deviation
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		project := in.ProjectID
		if itemID != nil {
			item, err := s.items.ByID(ctx, tx, *itemID)
			if err != nil {
				return err
			}
			project = item.ProjectID
		}

		subject, err := s.subject(ctx, tx, By(p), project)
		if err != nil {
			return err
		}
		if d := policy.CheckCreate(policy.KindDeviation, subject); d != nil {
			return d
		}

		now := time.Now()
		d := &pl.Deviation{
			ID:         uuid.Must(uuid.NewV7()),
			ProjectID:  project,
			WorkItemID: itemID,
			Summary:    strings.TrimSpace(in.Summary),
			Severity:   severity,
			ActorID:    p.WritingActorID(),
			CreatedAt:  now,
		}
		if err := pl.ValidateDeviation(d); err != nil {
			return err
		}

		if body := strings.TrimSpace(in.Body); body != "" {
			w := &pl.Worklog{
				ID:         uuid.Must(uuid.NewV7()),
				ProjectID:  project,
				WorkItemID: itemID,
				ActorID:    p.WritingActorID(),
				Kind:       pl.WorklogDeviation,
				Body:       body,
				CreatedAt:  now,
			}
			if err := pl.ValidateWorklog(w); err != nil {
				return err
			}
			if err := s.worklogs.Create(ctx, tx, w); err != nil {
				return err
			}
			if s.indexer != nil {
				if err := s.indexer.IndexWorklog(ctx, tx, w); err != nil {
					return err
				}
			}
			d.WorklogID = &w.ID
		}

		if err := s.deviations.Create(ctx, tx, d); err != nil {
			return err
		}

		created = d
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &project,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionDeviationCreated,
			SubjectID:  &d.ID,
			After:      publicDeviation(d),
		})
	})
	if err != nil {
		return nil, err
	}
	// Re-read so the caller gets the item ref the insert does not know. A
	// deviation that comes back naming a UUID instead of ZP-14 is a deviation
	// somebody has to go and look up before they can act on it.
	return s.deviations.ByID(ctx, s.db.Pool, created.ID)
}

// ListDeviations returns a project's deviations.
func (s *Service) ListDeviations(ctx context.Context, projectID uuid.UUID, f repo.DeviationFilter, p policy.Principal) ([]*pl.Deviation, error) {
	if err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.deviations.ListByProject(ctx, s.db.Pool, projectID, f)
}

// UpdateDeviationInput is a patch.
//
// `resolved_at` is not on the wire: setting a resolution resolves it, and
// clearing the resolution reopens it. Two fields that must agree are two fields
// that eventually will not, and the one that would be wrong is the timestamp
// the retrospective reads.
type UpdateDeviationInput struct {
	Fields []string

	Summary    *string
	Severity   *string
	Resolution *string
}

// UpdateDeviation patches a deviation.
//
// Ownership is split (§4.2): the reporter owns the summary and the severity
// because they saw it; a manager owns the resolution because deciding what to
// do about a divergence is a planning act.
func (s *Service) UpdateDeviation(ctx context.Context, id uuid.UUID, in UpdateDeviationInput, p policy.Principal) (*pl.Deviation, error) {
	var updated *pl.Deviation

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		d, err := s.deviations.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		subject, err := s.subject(ctx, tx, By(p), d.ProjectID)
		if err != nil {
			return err
		}
		// The reporter is the actor that raised it. An agent that raised a
		// deviation on a user's instruction is a different actor from the user,
		// and deliberately so: the user did not report it, the agent did.
		subject.IsReporter = d.ActorID == p.ActorID || d.ActorID == p.WritingActorID()
		if den := policy.CheckWrite(policy.KindDeviation, in.Fields, subject); den != nil {
			return den
		}

		before := publicDeviation(d)
		wasResolved := d.Resolved()

		if in.Summary != nil {
			d.Summary = strings.TrimSpace(*in.Summary)
		}
		if in.Severity != nil {
			d.Severity = pl.Severity(strings.TrimSpace(*in.Severity))
		}
		if in.Resolution != nil {
			d.Resolution = strings.TrimSpace(*in.Resolution)
			switch {
			case d.Resolution != "" && d.ResolvedAt == nil:
				now := time.Now()
				d.ResolvedAt = &now
			case d.Resolution == "":
				// Reopened. The timestamp goes with the resolution rather than
				// being kept as a ghost of one that no longer exists.
				d.ResolvedAt = nil
			}
		}

		if err := pl.ValidateDeviation(d); err != nil {
			return err
		}
		if err := s.deviations.Update(ctx, tx, d); err != nil {
			return err
		}

		updated = d
		after := publicDeviation(d)
		entry := func(action audit.Action, b, a any) error {
			return audit.Write(ctx, tx, audit.Entry{
				ProjectID:  &d.ProjectID,
				ActorID:    p.WritingActorID(),
				OnBehalfOf: p.OnBehalfOf(),
				Action:     action,
				SubjectID:  &d.ID,
				Before:     b,
				After:      a,
			})
		}

		if !wasResolved && d.Resolved() {
			if err := entry(audit.ActionDeviationResolved,
				map[string]any{"resolved": false},
				map[string]any{"resolved": true, "resolution": d.Resolution}); err != nil {
				return err
			}
		}
		if wasResolved && !d.Resolved() {
			// Reopening is the same transition read backwards, and it is at
			// least as interesting as the resolution: something judged settled
			// turned out not to be.
			if err := entry(audit.ActionDeviationResolved,
				map[string]any{"resolved": true, "resolution": before["resolution"]},
				map[string]any{"resolved": false}); err != nil {
				return err
			}
		}
		if !changedBeyondStatus(before, after, "resolution", "resolved") {
			return nil
		}
		return entry(audit.ActionDeviationUpdated, before, after)
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteDeviation soft-deletes a deviation.
func (s *Service) DeleteDeviation(ctx context.Context, id uuid.UUID, p policy.Principal) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		d, err := s.deviations.ByID(ctx, tx, id)
		if err != nil {
			return err
		}
		subject, err := s.subject(ctx, tx, By(p), d.ProjectID)
		if err != nil {
			return err
		}
		if den := policy.CheckDelete(policy.KindDeviation, subject); den != nil {
			return den
		}
		if err := s.deviations.Delete(ctx, tx, id); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &d.ProjectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionDeviationDeleted,
			SubjectID:  &d.ID,
			Before:     publicDeviation(d),
		})
	})
}

func publicDeviation(d *pl.Deviation) map[string]any {
	return map[string]any{
		"id":           d.ID,
		"work_item_id": uuidString(d.WorkItemID),
		"summary":      d.Summary,
		"severity":     string(d.Severity),
		"resolution":   d.Resolution,
		"resolved":     d.Resolved(),
	}
}
