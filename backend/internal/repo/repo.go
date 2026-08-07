// Package repo is SQL and nothing else. One file per aggregate.
//
// Two rules hold across every file here, and both are load-bearing:
//
//  1. **No repository opens a transaction.** They take a store.Querier, which
//     both the pool and a pgx.Tx satisfy. One logical operation is one
//     transaction and one audit entry, and only the service layer knows where
//     that boundary is.
//
//  2. **No repository exposes a hard delete.** Delete sets deleted_at; every
//     read filters it. A hard delete removes the row *and* orphans every audit
//     entry pointing at it, which is the one failure that makes a retrospective
//     unanswerable (CONTRACT §6.5 constraint 1). This is established here, with
//     four tables, rather than in Phase 4 with ten — one of which would by then
//     have grown a DELETE FROM.
//
// Rule 2 is enforced by a test that scans this package's source for DELETE FROM
// and fails on a hit. That test is why the rule is not merely a comment.
package repo

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
)

// notFound maps pgx's no-rows into the domain sentinel, so no caller above this
// package imports pgx to find out whether something exists. Any other error, and
// nil, pass through unchanged.
func notFound(err error, kind string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return missing(kind)
	}
	return err
}

// missing builds the sentinel directly, for the update and delete paths where
// "no rows affected" is the signal rather than a scan error. Deliberately
// separate from notFound: passing a nil error to that helper returns nil, which
// would turn every not-found write into a silent success.
func missing(kind string) error {
	return fmt.Errorf("%w: %s", domain.ErrNotFound, kind)
}

// conflict maps a unique-constraint violation onto the domain sentinel, naming
// the constraint so the service can turn it into a message about the field the
// user actually typed.
func conflict(err error, what string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: %s", domain.ErrConflict, what)
	}
	return err
}
