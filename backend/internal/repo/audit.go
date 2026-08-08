package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// AuditLogs reads the append-only record.
//
// Only reads. There is no Write here and there must not be one: entries are
// written by internal/audit, inside the transaction of the operation they
// describe, through a typed action enum. A second write path — one that took a
// string, or ran outside the transaction — is how the log acquires entries for
// operations that rolled back, and how it acquires actions the v0.1 export
// cannot find (§6.5, D13).
//
// Every query here joins the actor and, when there is one, the user behind it.
// A UUID answers "who changed this" the way a serial number answers "who is on
// the phone".
type AuditLogs struct{}

// AuditActor is who performed an entry, named.
type AuditActor struct {
	ID   uuid.UUID
	Kind string
	// Name is the user's display name for a human, the agent's name for an
	// agent, and "system" for the harness.
	Name string
	// ModelRef is set for agents. Which model made a change is part of who made
	// it: the same agent on a different model is a different reader of the same
	// prompt.
	ModelRef string
}

// AuditEntry is one record, joined and ready to read.
type AuditEntry struct {
	ID           uuid.UUID
	ProjectID    *uuid.UUID
	Actor        AuditActor
	OnBehalfOf   *uuid.UUID
	OnBehalfName string
	Action       string
	SubjectKind  string
	SubjectID    *uuid.UUID
	Before       json.RawMessage
	After        json.RawMessage
	RequestID    string
	At           time.Time
}

// AuditFilter is the query surface of GET /projects/{pid}/audit.
type AuditFilter struct {
	// SubjectKind and SubjectID narrow to one thing's history. The kind alone
	// is "everything that happened to any work item"; the pair is "everything
	// that happened to this one".
	SubjectKind string
	SubjectID   *uuid.UUID
	ActorID     *uuid.UUID
	Action      string
	Since       *time.Time
	Until       *time.Time
	Limit       int
}

const auditSelect = `
	SELECT a.id, a.project_id, a.actor_id, ac.kind,
	       coalesce(u.display_name, ac.agent_name, ac.kind), coalesce(ac.model_ref, ''),
	       a.on_behalf_of, coalesce(bu.display_name, ''),
	       a.action, a.subject_kind, a.subject_id, a.before, a.after, a.request_id, a.at
	  FROM audit_log a
	  JOIN actors ac ON ac.id = a.actor_id
	  LEFT JOIN users u  ON u.id = ac.user_id
	  LEFT JOIN users bu ON bu.id = a.on_behalf_of`

// List returns a project's audit entries, newest first.
//
// Newest first because the question is nearly always "what just happened", and
// paging backwards through an ascending list to find out is the sort of thing
// that makes people stop looking.
func (AuditLogs) List(ctx context.Context, q store.Querier, projectID uuid.UUID, f AuditFilter) ([]AuditEntry, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	sql := auditSelect + `
		 WHERE a.project_id = $1
		   AND ($2 = '' OR a.subject_kind = $2)
		   AND ($3::uuid IS NULL OR a.subject_id = $3)
		   AND ($4::uuid IS NULL OR a.actor_id = $4)
		   AND ($5 = '' OR a.action = $5)
		   AND ($6::timestamptz IS NULL OR a.at >= $6)
		   AND ($7::timestamptz IS NULL OR a.at <= $7)
		 ORDER BY a.at DESC, a.id DESC
		 LIMIT $8`

	rows, err := q.Query(ctx, sql, projectID, f.SubjectKind, f.SubjectID, f.ActorID,
		f.Action, f.Since, f.Until, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AuditEntry
	for rows.Next() {
		e, err := scanAuditEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MaxID returns the newest entry id for a project.
//
// The ids are UUIDv7, so they sort by creation. That is what makes "everything
// since" a comparison on the primary key rather than a timestamp range, which
// would double-count or skip entries written in the same millisecond.
func (AuditLogs) MaxID(ctx context.Context, q store.Querier, projectID uuid.UUID) (uuid.UUID, bool, error) {
	const sql = `SELECT max(id) FROM audit_log WHERE project_id = $1`
	var id *uuid.UUID
	if err := q.QueryRow(ctx, sql, projectID).Scan(&id); err != nil {
		return uuid.Nil, false, err
	}
	if id == nil {
		return uuid.Nil, false, nil
	}
	return *id, true, nil
}

// AuditSummary is what happened in a project over a range of the log.
type AuditSummary struct {
	// Total is how many entries matched.
	Total int
	// Counts is per action, so a commit message can say what kind of change it
	// is carrying rather than only that there was one.
	Counts map[string]int
	// Actors is everyone who contributed, deduplicated.
	Actors []AuditActor
	// MaxID is the newest entry seen, which becomes the next watermark.
	MaxID uuid.UUID
}

// SummarySince describes everything a project recorded after a given entry,
// restricted to a set of actions.
//
// `since` may be uuid.Nil, meaning "from the beginning". Used by the git
// projection to decide whose name a commit carries and what its message says —
// and restricted to actions the projection actually carries, so that a worklog
// nobody projected does not make the tree look stale.
func (AuditLogs) SummarySince(ctx context.Context, q store.Querier, projectID uuid.UUID,
	since uuid.UUID, actions []string) (AuditSummary, error) {

	var sinceArg *uuid.UUID
	if since != uuid.Nil {
		sinceArg = &since
	}
	sql := `
		SELECT a.action, a.actor_id, ac.kind,
		       coalesce(u.display_name, ac.agent_name, ac.kind), coalesce(ac.model_ref, ''), a.id
		  FROM audit_log a
		  JOIN actors ac ON ac.id = a.actor_id
		  LEFT JOIN users u ON u.id = ac.user_id
		 WHERE a.project_id = $1
		   AND ($2::uuid IS NULL OR a.id > $2)
		   AND (cardinality($3::text[]) = 0 OR a.action = ANY($3))`

	rows, err := q.Query(ctx, sql, projectID, sinceArg, actions)
	if err != nil {
		return AuditSummary{}, err
	}
	defer rows.Close()

	out := AuditSummary{Counts: map[string]int{}}
	seen := map[uuid.UUID]bool{}
	for rows.Next() {
		var (
			action string
			actor  AuditActor
			id     uuid.UUID
		)
		if err := rows.Scan(&action, &actor.ID, &actor.Kind, &actor.Name, &actor.ModelRef, &id); err != nil {
			return AuditSummary{}, err
		}
		out.Total++
		out.Counts[action]++
		if !seen[actor.ID] {
			seen[actor.ID] = true
			out.Actors = append(out.Actors, actor)
		}
		if id.String() > out.MaxID.String() {
			out.MaxID = id
		}
	}
	if err := rows.Err(); err != nil {
		return AuditSummary{}, err
	}
	sortAuditActors(out.Actors)
	return out, nil
}

func scanAuditEntry(row scanner) (AuditEntry, error) {
	var (
		e      AuditEntry
		before []byte
		after  []byte
	)
	err := row.Scan(&e.ID, &e.ProjectID, &e.Actor.ID, &e.Actor.Kind, &e.Actor.Name, &e.Actor.ModelRef,
		&e.OnBehalfOf, &e.OnBehalfName, &e.Action, &e.SubjectKind, &e.SubjectID,
		&before, &after, &e.RequestID, &e.At)
	if err != nil {
		return AuditEntry{}, fmt.Errorf("reading an audit entry: %w", err)
	}
	e.Before, e.After = json.RawMessage(before), json.RawMessage(after)
	return e, nil
}

// sortAuditActors orders humans before agents before the system, then by name,
// so a commit message reads the same way twice.
func sortAuditActors(in []AuditActor) {
	rank := func(kind string) int {
		switch kind {
		case "human":
			return 0
		case "agent":
			return 1
		default:
			return 2
		}
	}
	for i := 1; i < len(in); i++ {
		for j := i; j > 0; j-- {
			a, b := in[j-1], in[j]
			if rank(a.Kind) < rank(b.Kind) || (rank(a.Kind) == rank(b.Kind) && a.Name <= b.Name) {
				break
			}
			in[j-1], in[j] = in[j], in[j-1]
		}
	}
}
