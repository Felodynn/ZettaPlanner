package repo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Leases is the diagram_leases aggregate.
//
// Leases, not locks or CRDTs (D9). CRDTs on a typed graph is a project, not a
// feature; a lease with a heartbeat plus an atomic whole-diagram save gives one
// revision per human action, which is what makes history and audit readable.
//
// Expiry is evaluated on every read rather than by a sweeper. A crashed holder
// therefore stops blocking the diagram the moment its lease lapses, with no
// background job that can itself be down.
type Leases struct{}

// Lease is one held edit session.
type Lease struct {
	DiagramID    uuid.UUID
	ActorID      uuid.UUID
	BaseRevision int
	AcquiredAt   time.Time
	HeartbeatAt  time.Time
	ExpiresAt    time.Time

	// HolderName is filled in on read so a 409 can say who is holding it. A
	// refusal that names nobody leaves the caller with nothing to do.
	HolderName string
	HolderKind string
}

// Live reports whether the lease still holds at now.
func (l *Lease) Live(now time.Time) bool { return l != nil && now.Before(l.ExpiresAt) }

// Get returns the diagram's lease, live or lapsed, or nil if there is none.
func (Leases) Get(ctx context.Context, q store.Querier, diagramID uuid.UUID) (*Lease, error) {
	const sql = `
		SELECT l.diagram_id, l.actor_id, l.base_revision, l.acquired_at, l.heartbeat_at, l.expires_at,
		       coalesce(u.display_name, a.agent_name, 'the system'), a.kind
		  FROM diagram_leases l
		  JOIN actors a ON a.id = l.actor_id
		  LEFT JOIN users u ON u.id = a.user_id
		 WHERE l.diagram_id = $1`
	var l Lease
	err := q.QueryRow(ctx, sql, diagramID).Scan(&l.DiagramID, &l.ActorID, &l.BaseRevision,
		&l.AcquiredAt, &l.HeartbeatAt, &l.ExpiresAt, &l.HolderName, &l.HolderKind)
	if err != nil {
		if e := notFound(err, "lease"); isNotFound(e) {
			return nil, nil
		} else if e != nil {
			return nil, e
		}
	}
	return &l, nil
}

// Acquire takes the lease, or reports the live holder.
//
// One statement: the insert takes the row when it is free, and the ON CONFLICT
// clause takes it over when the existing row has expired. Read-then-write would
// let two callers both observe an expired lease and both take it.
func (Leases) Acquire(ctx context.Context, q store.Querier, diagramID, actorID uuid.UUID, baseRevision int, ttl time.Duration) (bool, error) {
	now := time.Now()
	const sql = `
		INSERT INTO diagram_leases (diagram_id, actor_id, base_revision, acquired_at, heartbeat_at, expires_at)
		VALUES ($1, $2, $3, $4, $4, $5)
		ON CONFLICT (diagram_id) DO UPDATE
		   SET actor_id = EXCLUDED.actor_id,
		       base_revision = EXCLUDED.base_revision,
		       acquired_at = EXCLUDED.acquired_at,
		       heartbeat_at = EXCLUDED.heartbeat_at,
		       expires_at = EXCLUDED.expires_at
		 WHERE diagram_leases.expires_at <= $4
		    OR diagram_leases.actor_id = EXCLUDED.actor_id`
	tag, err := q.Exec(ctx, sql, diagramID, actorID, baseRevision, now, now.Add(ttl))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Heartbeat extends a lease the caller still holds.
//
// Returns false when the lease has lapsed or belongs to someone else, so the
// client learns it has lost the session at heartbeat time rather than at save
// time, with its work still on screen.
func (Leases) Heartbeat(ctx context.Context, q store.Querier, diagramID, actorID uuid.UUID, ttl time.Duration) (bool, error) {
	now := time.Now()
	const sql = `
		UPDATE diagram_leases
		   SET heartbeat_at = $3, expires_at = $4
		 WHERE diagram_id = $1 AND actor_id = $2 AND expires_at > $3`
	tag, err := q.Exec(ctx, sql, diagramID, actorID, now, now.Add(ttl))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Release drops a lease the caller holds.
//
// A genuine row removal, and not history: the lease table is current state,
// acquisition and release are both audited, and diagram_leases is not in
// CONTRACT §6.5's list of soft-deleted tables. A lapsed lease left in place
// would have to be filtered by expiry on every read forever.
func (Leases) Release(ctx context.Context, q store.Querier, diagramID, actorID uuid.UUID) (bool, error) {
	const sql = `DELETE FROM diagram_leases WHERE diagram_id = $1 AND actor_id = $2` // zp:hard-delete-ok
	tag, err := q.Exec(ctx, sql, diagramID, actorID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ReleaseAny drops a lease regardless of holder. Used when a diagram is deleted.
func (Leases) ReleaseAny(ctx context.Context, q store.Querier, diagramID uuid.UUID) error {
	const sql = `DELETE FROM diagram_leases WHERE diagram_id = $1` // zp:hard-delete-ok
	_, err := q.Exec(ctx, sql, diagramID)
	return err
}
