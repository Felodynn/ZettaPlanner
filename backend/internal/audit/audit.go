// Package audit writes the append-only record.
//
// The record is v0; the CSV export is v0.1 (D13). Only one of those can be
// backfilled, so every constraint that makes the export possible later is
// enforced now: a typed action, a populated request_id, one entry per logical
// operation, and no way to delete or update what has been written.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// Entry is one audit record.
//
// There is no SubjectKind field: it is derived from the Action, so an entry
// whose action and subject kind disagree is not representable.
type Entry struct {
	// ProjectID is nil only for deployment-level actions (user management,
	// model configuration).
	ProjectID *uuid.UUID

	// ActorID is the principal that performed the write. For an agent write
	// this is the agent's actor row, never the user's (D11).
	ActorID uuid.UUID

	// OnBehalfOf is the instructing user when an agent acts. Set together with
	// an agent ActorID and nil otherwise; the pair is what lets a reader see
	// both who asked and what ran.
	OnBehalfOf *uuid.UUID

	Action    Action
	SubjectID *uuid.UUID

	// Before and After are marshalled to JSONB. For a transition they hold the
	// two states; for a create, Before is nil; for a delete, After is nil.
	Before any
	After  any

	// At defaults to now.
	At time.Time
}

// Write appends one entry, inside the caller's transaction.
//
// A logical operation touching several rows writes ONE entry with the shared
// request_id, not one per row. That distinction is what makes the transition
// export readable rather than a wall of near-duplicates.
func Write(ctx context.Context, q store.Querier, e Entry) error {
	if !e.Action.Valid() {
		return fmt.Errorf("audit action %q is not registered in the taxonomy; add it to internal/audit/action.go rather than writing a raw string, or the v0.1 export cannot find these events", e.Action)
	}
	if e.ActorID == uuid.Nil {
		return fmt.Errorf("audit entry for %s has no actor; every write carries a principal", e.Action)
	}

	requestID := telemetry.RequestID(ctx)
	if requestID == "" {
		// NOT NULL in the schema, so this would fail at the insert anyway. It
		// is caught here so the message names the cause rather than the column.
		return fmt.Errorf("audit entry for %s has no request_id in context; it is generated in middleware and threaded through — a code path that writes without one cannot be correlated", e.Action)
	}

	before, err := encode(e.Before)
	if err != nil {
		return fmt.Errorf("encoding audit before-state for %s: %w", e.Action, err)
	}
	after, err := encode(e.After)
	if err != nil {
		return fmt.Errorf("encoding audit after-state for %s: %w", e.Action, err)
	}

	at := e.At
	if at.IsZero() {
		at = time.Now()
	}

	const q1 = `
		INSERT INTO audit_log
		  (id, project_id, actor_id, on_behalf_of, action, subject_kind, subject_id,
		   before, after, request_id, at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err = q.Exec(ctx, q1,
		uuid.Must(uuid.NewV7()),
		e.ProjectID,
		e.ActorID,
		e.OnBehalfOf,
		string(e.Action),
		e.Action.SubjectKind(),
		e.SubjectID,
		before,
		after,
		requestID,
		at,
	)
	if err != nil {
		return fmt.Errorf("writing audit entry %s: %w", e.Action, err)
	}
	return nil
}

func encode(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// Ref is a small helper for the pointer fields.
func Ref(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
