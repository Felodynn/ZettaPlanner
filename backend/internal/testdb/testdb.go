// Package testdb hands each test package its own scratch database.
//
// Go runs different packages' tests concurrently. Two packages resetting and
// migrating the same database interleave, and the resulting failures look like
// product bugs — "relation users does not exist", "this deployment already has
// 1 accounts" — which is a bad way to spend an afternoon. One database per
// package removes the shared state rather than serialising around it.
package testdb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// resetSQL empties the schema without dropping it.
//
// Deliberately not DROP SCHEMA public CASCADE: that takes the pgvector
// extension with it, and creating an extension needs superuser. The test role
// is the application role, which is exactly the privilege level the migration
// has to work at.
const resetSQL = `
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' LOOP
    EXECUTE format('DROP TABLE IF EXISTS public.%I CASCADE', r.tablename);
  END LOOP;
  FOR r IN
    SELECT p.oid::regprocedure AS sig
    FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'public' AND p.proname LIKE 'zp\_%'
  LOOP
    EXECUTE format('DROP FUNCTION IF EXISTS %s CASCADE', r.sig);
  END LOOP;
END $$;`

// BaseURL returns ZP_TEST_DATABASE_URL, skipping the test if it is unset.
func BaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("ZP_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ZP_TEST_DATABASE_URL is not set; skipping the database tests")
	}
	return url
}

// Fresh returns an empty database scoped to suffix, with no schema applied.
//
// The database is created on demand from template1, which is why the setup
// instructions install pgvector there: it makes every scratch database inherit
// the extension without another superuser step.
func Fresh(t *testing.T, suffix string) (*store.DB, context.Context) {
	t.Helper()
	ctx := telemetry.WithRequestID(context.Background(), telemetry.NewRequestID())
	base := BaseURL(t)

	name := databaseName(base, suffix)
	ensureDatabase(t, ctx, base, name)

	db := connect(t, ctx, base, name)
	if _, err := db.Pool.Exec(ctx, resetSQL); err != nil {
		t.Fatalf("resetting %s: %v", name, err)
	}
	return db, ctx
}

// Connect opens the scratch database for suffix without resetting it. For the
// concurrency tests, which need several independent pools onto one database.
func Connect(t *testing.T, suffix string) *store.DB {
	t.Helper()
	ctx := context.Background()
	base := BaseURL(t)
	return connect(t, ctx, base, databaseName(base, suffix))
}

func databaseName(base, suffix string) string {
	cfg, err := pgxpool.ParseConfig(base)
	if err != nil {
		return "zettaplanner_test_" + sanitise(suffix)
	}
	return cfg.ConnConfig.Database + "_" + sanitise(suffix)
}

func sanitise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func ensureDatabase(t *testing.T, ctx context.Context, base, name string) {
	t.Helper()
	admin := connectTo(t, ctx, base, "")
	defer admin.Close()

	var exists bool
	if err := admin.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("checking for %s: %v", name, err)
	}
	if exists {
		return
	}
	// CREATE DATABASE cannot be parameterised or run inside a transaction. The
	// name comes from sanitise, so it holds only [a-z0-9_].
	if _, err := admin.Pool.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, name)); err != nil {
		// A parallel package may have won the race between the check and here.
		if !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("creating %s: %v. If this is a permissions error, the base database's owner needs CREATEDB.", name, err)
		}
	}
}

func connect(t *testing.T, ctx context.Context, base, name string) *store.DB {
	t.Helper()
	db := connectTo(t, ctx, base, name)
	t.Cleanup(db.Close)
	return db
}

// connectTo opens base, optionally overriding the database name. Overriding the
// parsed config avoids doing string surgery on a libpq URL, which comes in more
// shapes than it looks like it does.
func connectTo(t *testing.T, ctx context.Context, base, name string) *store.DB {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(base)
	if err != nil {
		t.Fatalf("parsing ZP_TEST_DATABASE_URL: %v", err)
	}
	if name != "" {
		cfg.ConnConfig.Database = name
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting to %s: %v", cfg.ConnConfig.Database, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("connecting to %s: %v", cfg.ConnConfig.Database, err)
	}
	return &store.DB{Pool: pool}
}
