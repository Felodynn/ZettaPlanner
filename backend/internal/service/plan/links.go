package plan

import (
	"context"
	"fmt"
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

// SetDependenciesInput replaces an item's outgoing dependencies.
//
// A nil slice means "leave this kind alone"; an empty one means "clear it". The
// distinction matters because the endpoint is a PUT over two kinds at once, and
// sending only `blocks` must not silently wipe `relates`.
type SetDependenciesInput struct {
	Blocks  *[]uuid.UUID
	Relates *[]uuid.UUID
}

// SetDependencies replaces a work item's dependencies and rejects cycles.
//
// Manager-owned: `dependencies` is one of the fields in CONTRACT §4.2, and a
// developer's route to changing one is a proposal.
func (s *Service) SetDependencies(ctx context.Context, itemID uuid.UUID, in SetDependenciesInput, p policy.Principal) ([]pl.Dependency, error) {
	var out []pl.Dependency

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		item, err := s.items.ByID(ctx, tx, itemID)
		if err != nil {
			return err
		}
		subject, err := s.forItem(ctx, tx, p, item)
		if err != nil {
			return err
		}
		if d := policy.CheckWrite(policy.KindWorkItem, []string{"dependencies"}, subject); d != nil {
			return d
		}

		before, err := s.deps.Outgoing(ctx, tx, itemID)
		if err != nil {
			return err
		}

		now := time.Now()
		for _, set := range []struct {
			kind    pl.DependencyKind
			targets *[]uuid.UUID
		}{{pl.DepBlocks, in.Blocks}, {pl.DepRelates, in.Relates}} {
			if set.targets == nil {
				continue
			}
			targets, err := s.validDependencyTargets(ctx, tx, item, *set.targets)
			if err != nil {
				return err
			}
			if err := s.deps.Set(ctx, tx, itemID, set.kind, targets, p.WritingActorID(), now); err != nil {
				return err
			}
		}

		// After the write, inside the transaction. Checking beforehand would
		// mean modelling the proposed graph in Go; checking here lets Postgres
		// answer against the rows that actually exist, and the rollback undo
		// them if the answer is a cycle.
		if in.Blocks != nil {
			if err := s.rejectCycle(ctx, tx, itemID); err != nil {
				return err
			}
		}

		out, err = s.deps.Outgoing(ctx, tx, itemID)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &item.ProjectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionDependenciesSet,
			SubjectID:  &item.ID,
			Before:     publicDependencies(before),
			After:      publicDependencies(out),
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// validDependencyTargets rejects targets that are not live items of the same
// project, and the item itself.
//
// Named individually rather than reported as "one of these is invalid". The
// caller sent a list; telling them which entry is wrong is the difference
// between a fixable error and a bisect.
func (s *Service) validDependencyTargets(ctx context.Context, q store.Querier, item *pl.WorkItem, targets []uuid.UUID) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(targets))

	for _, id := range targets {
		if id == item.ID {
			return nil, fmt.Errorf("%w: %s cannot depend on itself", domain.ErrInvalid, item.Ref)
		}
		if seen[id] {
			continue
		}
		seen[id] = true

		target, err := s.items.ByID(ctx, q, id)
		if err != nil {
			if isNotFound(err) {
				return nil, fmt.Errorf("%w: there is no live work item %s in this project", domain.ErrNotFound, id)
			}
			return nil, err
		}
		if target.ProjectID != item.ProjectID {
			return nil, fmt.Errorf("%w: %s belongs to a different project; dependencies do not cross projects",
				domain.ErrInvalid, target.Ref)
		}
		out = append(out, id)
	}
	return out, nil
}

// rejectCycle fails the transaction if the write closed a loop in `blocks`.
func (s *Service) rejectCycle(ctx context.Context, q store.Querier, itemID uuid.UUID) error {
	path, truncated, err := s.deps.FindCycle(ctx, q, itemID)
	if err != nil {
		return err
	}
	if truncated {
		return fmt.Errorf("%w: the `blocks` chain from this item runs deeper than %d links, so whether this edge "+
			"closes a loop could not be decided. The dependency was not saved. A chain that long is worth "+
			"breaking up regardless — nobody can hold it in their head either",
			domain.ErrInvalid, repo.MaxDependencyDepth)
	}
	if len(path) == 0 {
		return nil
	}

	refs, err := s.items.RefsFor(ctx, q, path)
	if err != nil {
		return err
	}
	named := make([]string, 0, len(path))
	for _, id := range path {
		ref, ok := refs[id]
		if !ok {
			ref = id.String()
		}
		named = append(named, ref)
	}
	return &pl.CycleError{Path: ring(named)}
}

// ring trims a walk down to the loop itself.
//
// The walk starts at the item being written and follows edges until one repeats,
// so the path may carry a tail before the loop closes. Printing that tail would
// name an item that is not in the cycle as though it were, which is exactly the
// item somebody would then go and edit.
func ring(path []string) []string {
	if len(path) < 2 {
		return path
	}
	last := path[len(path)-1]
	for i, v := range path {
		if v == last {
			return path[i:]
		}
	}
	return path
}

// SetEntityRefs replaces a work item's references to the architecture graph.
//
// Explicit, never derived (§3.5). Nothing here looks at a diagram, matches a
// name or consults an embedding: a guessed reference pulls the wrong slice of
// architecture into the next conversation while looking exactly like one
// somebody meant.
func (s *Service) SetEntityRefs(ctx context.Context, itemID uuid.UUID, entityIDs []uuid.UUID, p policy.Principal) ([]pl.EntityRef, error) {
	var out []pl.EntityRef

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		item, err := s.items.ByID(ctx, tx, itemID)
		if err != nil {
			return err
		}
		subject, err := s.forItem(ctx, tx, p, item)
		if err != nil {
			return err
		}
		if d := policy.CheckWrite(policy.KindWorkItem, []string{"entity_refs"}, subject); d != nil {
			return d
		}

		before, err := s.refs.ListByItem(ctx, tx, itemID)
		if err != nil {
			return err
		}

		live, err := s.refs.ExistingIDs(ctx, tx, item.ProjectID, entityIDs)
		if err != nil {
			return err
		}
		for _, id := range entityIDs {
			if !live[id] {
				return fmt.Errorf("%w: there is no live catalog entity %s in this project", domain.ErrNotFound, id)
			}
		}

		if err := s.refs.Set(ctx, tx, itemID, entityIDs, pl.RefImplements, p.WritingActorID()); err != nil {
			return err
		}

		out, err = s.refs.ListByItem(ctx, tx, itemID)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID:  &item.ProjectID,
			ActorID:    p.WritingActorID(),
			OnBehalfOf: p.OnBehalfOf(),
			Action:     audit.ActionEntityRefsSet,
			SubjectID:  &item.ID,
			Before:     publicRefs(before),
			After:      publicRefs(out),
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func publicDependencies(deps []pl.Dependency) map[string]any {
	blocks, relates := []string{}, []string{}
	for _, d := range deps {
		if d.Kind == pl.DepBlocks {
			blocks = append(blocks, d.ToRef)
		} else {
			relates = append(relates, d.ToRef)
		}
	}
	return map[string]any{"blocks": blocks, "relates": relates}
}

func publicRefs(refs []pl.EntityRef) map[string]any {
	slugs := []string{}
	for _, r := range refs {
		slugs = append(slugs, r.EntitySlug)
	}
	return map[string]any{"implements": slugs}
}
