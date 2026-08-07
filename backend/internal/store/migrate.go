package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationLockKey is the advisory lock every migration run takes before it
// reads anything.
//
// Two processes opening the same fresh database will otherwise both see version
// 0, both run 0001, and the second dies on a unique constraint — or worse,
// half-applies. The lock is taken first, inside the same transaction, so the
// version read is already serialised.
const migrationLockKey int64 = 0x5A50_4C41_4E00_0001 // "ZPLAN" + 1

// Migration is one versioned schema change.
type Migration struct {
	Version int
	Name    string
	Up      string
	Down    string
}

// MigrateUp applies every pending migration in one transaction.
//
// Idempotent: running it twice applies nothing the second time. Safe under
// concurrency: the second process blocks on the advisory lock, then finds
// nothing to do.
func MigrateUp(ctx context.Context, db *DB, log *slog.Logger) ([]Migration, error) {
	if log == nil {
		log = slog.Default()
	}
	all, err := loadMigrations()
	if err != nil {
		return nil, err
	}

	var applied []Migration
	err = db.InTx(ctx, func(tx pgx.Tx) error {
		if err := lockAndPrepare(ctx, tx); err != nil {
			return err
		}
		done, err := appliedVersions(ctx, tx)
		if err != nil {
			return err
		}
		for _, m := range all {
			if done[m.Version] {
				continue
			}
			log.InfoContext(ctx, "applying migration", "version", m.Version, "name", m.Name)
			if _, err := tx.Exec(ctx, m.Up); err != nil {
				return fmt.Errorf("migration %04d_%s failed: %w", m.Version, m.Name, err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO schema_migrations (version, name, applied_at) VALUES ($1, $2, now())`,
				m.Version, m.Name); err != nil {
				return fmt.Errorf("recording migration %04d_%s: %w", m.Version, m.Name, err)
			}
			applied = append(applied, m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return applied, nil
}

// MigrateDown rolls back the most recently applied migration.
//
// A convenience for development. Nothing in production ever calls it: the
// audit log has a stated retention of never (CONTRACT §6.5 constraint 5), and
// a down migration that drops it is a data loss event wearing a maintenance
// costume.
func MigrateDown(ctx context.Context, db *DB, log *slog.Logger) (*Migration, error) {
	if log == nil {
		log = slog.Default()
	}
	all, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	byVersion := make(map[int]Migration, len(all))
	for _, m := range all {
		byVersion[m.Version] = m
	}

	var rolled *Migration
	err = db.InTx(ctx, func(tx pgx.Tx) error {
		if err := lockAndPrepare(ctx, tx); err != nil {
			return err
		}
		var version int
		var name string
		err := tx.QueryRow(ctx,
			`SELECT version, name FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version, &name)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the current schema version: %w", err)
		}
		m, ok := byVersion[version]
		if !ok {
			return fmt.Errorf("migration %04d is applied but its files are missing; the binary is older than the database", version)
		}
		if strings.TrimSpace(m.Down) == "" {
			return fmt.Errorf("migration %04d_%s has no down file", version, name)
		}
		log.InfoContext(ctx, "reverting migration", "version", version, "name", name)
		if _, err := tx.Exec(ctx, m.Down); err != nil {
			return fmt.Errorf("reverting %04d_%s: %w", version, name, err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, version); err != nil {
			return fmt.Errorf("clearing the migration record for %04d: %w", version, err)
		}
		rolled = &m
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rolled, nil
}

// SchemaVersion returns the highest applied migration version, or 0.
func SchemaVersion(ctx context.Context, q Querier) (int, error) {
	var version int
	err := q.QueryRow(ctx,
		`SELECT coalesce(max(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// lockAndPrepare takes the advisory lock and ensures the bookkeeping table
// exists. Lock first, read second — that ordering is the entire point.
func lockAndPrepare(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("taking the migration advisory lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		  version    INT PRIMARY KEY,
		  name       TEXT NOT NULL,
		  applied_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, tx pgx.Tx) (map[int]bool, error) {
	rows, err := tx.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("reading applied migrations: %w", err)
	}
	defer rows.Close()

	done := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		done[v] = true
	}
	return done, rows.Err()
}

// loadMigrations reads the embedded migration files, in version order.
func loadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("reading embedded migrations: %w", err)
	}

	byVersion := map[int]*Migration{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, direction, err := parseMigrationName(e.Name())
		if err != nil {
			return nil, err
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		m, ok := byVersion[version]
		if !ok {
			m = &Migration{Version: version, Name: name}
			byVersion[version] = m
		}
		if direction == "up" {
			m.Up = string(body)
		} else {
			m.Down = string(body)
		}
	}

	out := make([]Migration, 0, len(byVersion))
	for _, m := range byVersion {
		if strings.TrimSpace(m.Up) == "" {
			return nil, fmt.Errorf("migration %04d_%s has no .up.sql file", m.Version, m.Name)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// parseMigrationName splits NNNN_name.up.sql into its parts.
func parseMigrationName(file string) (version int, name, direction string, err error) {
	base := strings.TrimSuffix(file, ".sql")

	switch {
	case strings.HasSuffix(base, ".up"):
		direction = "up"
		base = strings.TrimSuffix(base, ".up")
	case strings.HasSuffix(base, ".down"):
		direction = "down"
		base = strings.TrimSuffix(base, ".down")
	default:
		return 0, "", "", fmt.Errorf("migration %q must end in .up.sql or .down.sql", file)
	}

	num, rest, ok := strings.Cut(base, "_")
	if !ok {
		return 0, "", "", fmt.Errorf("migration %q must be named NNNN_name.up.sql", file)
	}
	version, err = strconv.Atoi(num)
	if err != nil {
		return 0, "", "", fmt.Errorf("migration %q has a non-numeric version prefix", file)
	}
	return version, rest, direction, nil
}
