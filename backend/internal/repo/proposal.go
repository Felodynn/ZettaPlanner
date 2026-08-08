package repo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	pr "github.com/Felodynn/ZettaPlanner/backend/internal/domain/proposal"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Proposals is the governance aggregate.
//
// Only the reads context assembly needs exist here in Phase 5. The write side
// arrives in Phase 7 — but the reads have to exist now, because CONTRACT §5.2
// makes rejected proposals with their rationales part of the skeleton, and a
// skeleton with a slot that is wired up later is a slot somebody forgets to
// wire up. Until Phase 7 fills the table these return nothing, which is the
// correct answer for a project that has decided nothing yet.
type Proposals struct{}

// DecisionRow is a decided proposal as the skeleton needs it.
type DecisionRow struct {
	ID        uuid.UUID
	Title     string
	Rationale string
	// DecisionRationale is why it was accepted or refused. **Required on a
	// rejection** (§5.2): it is the field whose loss makes an agent re-propose
	// what was already refused.
	DecisionRationale string
	Status            string
	TargetKind        string
	DecidedBy         string
	DecidedAt         time.Time
}

const proposalColumns = `p.id, p.project_id, p.actor_id, p.on_behalf_of, p.turn_id, p.title,
                         p.rationale, p.target_kind, p.status, p.decided_by, p.decided_at,
                         coalesce(p.decision_rationale, ''), p.applied_revision, p.created_at`

// Create writes a proposal and its changes as one row set.
//
// The changes are part of the proposal, not attachments to it: a proposal is
// applied atomically as one revision or not at all (§5.1), so a half-written
// change list is a proposal that would apply something nobody proposed.
func (Proposals) Create(ctx context.Context, q store.Querier, p *pr.Proposal) error {
	const sql = `
		INSERT INTO proposals (id, project_id, actor_id, on_behalf_of, turn_id, title, rationale,
		                       target_kind, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	if _, err := q.Exec(ctx, sql, p.ID, p.ProjectID, p.ActorID, p.OnBehalfOf, p.TurnID,
		p.Title, p.Rationale, string(p.Target), string(p.Status), p.CreatedAt); err != nil {
		return err
	}

	const changeSQL = `
		INSERT INTO proposal_changes (id, proposal_id, seq, op, target_kind, target_id, before, after)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	for i := range p.Changes {
		c := &p.Changes[i]
		c.ID = uuid.Must(uuid.NewV7())
		c.ProposalID = p.ID
		c.Seq = i + 1
		before, err := marshalOrNil(c.Before)
		if err != nil {
			return err
		}
		after, err := marshalOrNil(c.After)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, changeSQL, c.ID, c.ProposalID, c.Seq, string(c.Op),
			string(c.Target), c.TargetID, before, after); err != nil {
			return err
		}
	}
	return nil
}

// ByID returns one proposal with its changes in order.
func (Proposals) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*pr.Proposal, error) {
	const sql = `
		SELECT ` + proposalColumns + `,
		       coalesce(ra.agent_name, coalesce(ru.display_name, '')), coalesce(du.display_name, '')
		  FROM proposals p
		  LEFT JOIN actors ra ON ra.id = p.actor_id
		  LEFT JOIN users  ru ON ru.id = ra.user_id
		  LEFT JOIN users  du ON du.id = p.decided_by
		 WHERE p.id = $1`
	p, err := scanProposal(q.QueryRow(ctx, sql, id))
	if err != nil {
		return nil, err
	}
	if p.Changes, err = (Proposals{}).changes(ctx, q, id); err != nil {
		return nil, err
	}
	return p, nil
}

// ListByProject returns a project's proposals, newest first, without changes.
func (Proposals) ListByProject(ctx context.Context, q store.Querier, projectID uuid.UUID, status pr.Status) ([]*pr.Proposal, error) {
	const sql = `
		SELECT ` + proposalColumns + `,
		       coalesce(ra.agent_name, coalesce(ru.display_name, '')), coalesce(du.display_name, '')
		  FROM proposals p
		  LEFT JOIN actors ra ON ra.id = p.actor_id
		  LEFT JOIN users  ru ON ru.id = ra.user_id
		  LEFT JOIN users  du ON du.id = p.decided_by
		 WHERE p.project_id = $1 AND ($2 = '' OR p.status = $2)
		 ORDER BY p.created_at DESC, p.id DESC`
	rows, err := q.Query(ctx, sql, projectID, string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*pr.Proposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (Proposals) changes(ctx context.Context, q store.Querier, proposalID uuid.UUID) ([]pr.Change, error) {
	const sql = `
		SELECT id, proposal_id, seq, op, target_kind, target_id, before, after
		  FROM proposal_changes WHERE proposal_id = $1 ORDER BY seq`
	rows, err := q.Query(ctx, sql, proposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []pr.Change
	for rows.Next() {
		var (
			c             pr.Change
			op, target    string
			before, after []byte
		)
		if err := rows.Scan(&c.ID, &c.ProposalID, &c.Seq, &op, &target, &c.TargetID, &before, &after); err != nil {
			return nil, err
		}
		c.Op = pr.Op(op)
		c.Target = pr.ChangeTarget(target)
		if err := unmarshalIfPresent(before, &c.Before); err != nil {
			return nil, err
		}
		if err := unmarshalIfPresent(after, &c.After); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func scanProposal(row scanner) (*pr.Proposal, error) {
	var (
		p              pr.Proposal
		target, status string
	)
	err := row.Scan(&p.ID, &p.ProjectID, &p.ActorID, &p.OnBehalfOf, &p.TurnID, &p.Title,
		&p.Rationale, &target, &status, &p.DecidedBy, &p.DecidedAt, &p.DecisionRationale,
		&p.AppliedRevision, &p.CreatedAt, &p.RaisedByName, &p.DecidedByName)
	if err != nil {
		return nil, notFound(err, "proposal")
	}
	p.Target = pr.TargetKind(target)
	p.Status = pr.Status(status)
	return &p, nil
}

func marshalOrNil(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return nil, nil
	}
	return json.Marshal(m)
}

func unmarshalIfPresent(raw []byte, into *map[string]any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, into)
}

// Decided returns a project's decided proposals of one status, most recent
// first.
func (Proposals) Decided(ctx context.Context, q store.Querier, projectID uuid.UUID, status string, limit int) ([]DecisionRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	const sql = `
		SELECT p.id, p.title, p.rationale, coalesce(p.decision_rationale, ''),
		       p.status, p.target_kind, coalesce(u.display_name, ''), coalesce(p.decided_at, p.created_at)
		  FROM proposals p
		  LEFT JOIN users u ON u.id = p.decided_by
		 WHERE p.project_id = $1 AND p.status = $2
		 ORDER BY coalesce(p.decided_at, p.created_at) DESC, p.id DESC
		 LIMIT $3`
	rows, err := q.Query(ctx, sql, projectID, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DecisionRow
	for rows.Next() {
		var d DecisionRow
		if err := rows.Scan(&d.ID, &d.Title, &d.Rationale, &d.DecisionRationale,
			&d.Status, &d.TargetKind, &d.DecidedBy, &d.DecidedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
