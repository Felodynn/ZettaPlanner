// Package store owns the database connection and the migration runner.
//
// No ORM. pgx with hand-written SQL, and a schema kept portable and legible —
// the whole product is the schema.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is the read/write surface a repository needs. Both *pgxpool.Pool and
// pgx.Tx satisfy it, which is what lets a repo be called inside or outside a
// transaction without knowing which.
//
// Repositories take a Querier. They never open transactions: a transaction is
// one logical operation and one audit entry, and only the service layer knows
// where that boundary is.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// DB is the process-wide pool.
type DB struct {
	Pool *pgxpool.Pool
}

// Open connects and verifies the connection.
func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parsing ZP_DATABASE_URL: %w", err)
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	if cfg.MaxConns < 8 {
		cfg.MaxConns = 8
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to Postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connecting to Postgres at %s: %w", redact(url), err)
	}
	return &DB{Pool: pool}, nil
}

// Close releases the pool.
func (db *DB) Close() {
	if db != nil && db.Pool != nil {
		db.Pool.Close()
	}
}

// InTx runs fn inside a transaction, committing on success and rolling back on
// any error or panic.
//
// One logical operation = one transaction = one audit entry. The audit write
// happens inside fn, against the same tx, so a write that lands without its
// audit row is not a state the database can reach.
func (db *DB) InTx(ctx context.Context, fn func(pgx.Tx) error) (err error) {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

// EmbeddingDim returns the declared dimension of the chunks.embedding column.
//
// Read at boot and handed to llm.Validate, so that changing the embedding model
// to one of a different size fails at startup rather than silently corrupting
// every subsequent retrieval (CONTRACT §8). Returns 0 if the table does not
// exist yet, which is the normal state before the first migration.
func (db *DB) EmbeddingDim(ctx context.Context) (int, error) {
	const q = `
		SELECT a.atttypmod
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = 'chunks'
		  AND a.attname = 'embedding'
		  AND n.nspname = current_schema()
		  AND NOT a.attisdropped`

	var dim int
	err := db.Pool.QueryRow(ctx, q).Scan(&dim)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading the chunks.embedding dimension: %w", err)
	}
	return dim, nil
}

// redact strips a password from a connection string for error messages.
func redact(url string) string {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return "the configured database"
	}
	c := cfg.ConnConfig
	if c.Host == "" {
		return c.Database
	}
	return fmt.Sprintf("%s@%s:%d/%s", c.User, c.Host, c.Port, c.Database)
}
