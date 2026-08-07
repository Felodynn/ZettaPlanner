package store_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// These tests run against a real Postgres. The database is the product; a mock
// of it would test nothing (BACKEND.md §1.2).
//
//	createdb zettaplanner_test && psql -d zettaplanner_test -c 'CREATE EXTENSION vector'
//	ZP_TEST_DATABASE_URL=postgres:///zettaplanner_test go test ./internal/store/

func testURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("ZP_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("ZP_TEST_DATABASE_URL is not set; skipping the database tests")
	}
	return url
}

// freshDB returns an empty database with no schema applied.
func freshDB(t *testing.T) (*store.DB, context.Context) {
	t.Helper()
	ctx := telemetry.WithRequestID(context.Background(), telemetry.NewRequestID())

	db, err := store.Open(ctx, testURL(t))
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(db.Close)

	// A migration test that inherits the previous run's schema is not testing
	// migration.
	//
	// Tables and our own functions only, deliberately not DROP SCHEMA public —
	// that would take the pgvector extension with it, and recreating an
	// extension needs superuser. The test role is the application role, which
	// is exactly the privilege level the migration has to work at.
	if _, err := db.Pool.Exec(ctx, resetSQL); err != nil {
		t.Fatalf("resetting the schema: %v", err)
	}
	return db, ctx
}

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

// migratedDB returns a database with migrations applied and the minimum
// fixtures every audit row needs.
func migratedDB(t *testing.T) (*store.DB, context.Context, fixtures) {
	t.Helper()
	db, ctx := freshDB(t)
	if _, err := store.MigrateUp(ctx, db, quietLog()); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return db, ctx, seed(t, ctx, db)
}

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

type fixtures struct {
	UserID    uuid.UUID
	ActorID   uuid.UUID
	ProjectID uuid.UUID
}

func seed(t *testing.T, ctx context.Context, db *store.DB) fixtures {
	t.Helper()
	f := fixtures{
		UserID:    uuid.Must(uuid.NewV7()),
		ActorID:   uuid.Must(uuid.NewV7()),
		ProjectID: uuid.Must(uuid.NewV7()),
	}
	now := time.Now()
	mustExec(t, ctx, db, `INSERT INTO users (id, email, display_name, password_hash, role, created_at, updated_at)
		VALUES ($1, 'seed@example.test', 'Seed', 'x', 'admin', $2, $2)`, f.UserID, now)
	mustExec(t, ctx, db, `INSERT INTO actors (id, kind, user_id, created_at) VALUES ($1, 'human', $2, $3)`,
		f.ActorID, f.UserID, now)
	mustExec(t, ctx, db, `INSERT INTO projects (id, key, name, status, created_at, updated_at)
		VALUES ($1, 'ZP', 'ZettaPlanner', 'active', $2, $2)`, f.ProjectID, now)
	return f
}

func mustExec(t *testing.T, ctx context.Context, db *store.DB, sql string, args ...any) {
	t.Helper()
	if _, err := db.Pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", strings.SplitN(strings.TrimSpace(sql), "\n", 2)[0], err)
	}
}

// ── migrations ───────────────────────────────────────────────────────────────

func TestMigrateUpIsIdempotent(t *testing.T) {
	db, ctx := freshDB(t)

	first, err := store.MigrateUp(ctx, db, quietLog())
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("first migrate applied nothing")
	}

	second, err := store.MigrateUp(ctx, db, quietLog())
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("second migrate applied %d migrations; it must apply none", len(second))
	}
}

// TestMigrateUpUnderConcurrency is the reason the runner takes a write lock
// before it reads the version. Without the lock both processes see version 0,
// both run 0001, and the second dies on a unique constraint.
func TestMigrateUpUnderConcurrency(t *testing.T) {
	db, ctx := freshDB(t)
	db.Close() // each racer opens its own pool, as separate processes would

	const racers = 4
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		applied int
		errs    []error
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			own, err := store.Open(ctx, testURL(t))
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			defer own.Close()

			got, err := store.MigrateUp(ctx, own, quietLog())
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			applied += len(got)
		}()
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("concurrent migration failed: %v", errs)
	}

	reopened, err := store.Open(ctx, testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	total, err := countMigrationFiles(ctx, reopened)
	if err != nil {
		t.Fatal(err)
	}
	if applied != total {
		t.Errorf("%d racers applied %d migrations in total; exactly %d should have been applied once each",
			racers, applied, total)
	}
}

func countMigrationFiles(ctx context.Context, db *store.DB) (int, error) {
	var n int
	err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n)
	return n, err
}

// ── audit permanence (CONTRACT §6.5) ─────────────────────────────────────────

func TestAuditLogIsAppendOnly(t *testing.T) {
	db, ctx, f := migratedDB(t)

	writeEntry(t, ctx, db, f, audit.ActionProjectCreated)

	for _, stmt := range []string{
		`UPDATE audit_log SET action = 'tampered'`,
		`DELETE FROM audit_log`,
		`TRUNCATE audit_log`,
	} {
		if _, err := db.Pool.Exec(ctx, stmt); err == nil {
			t.Errorf("%q succeeded; audit_log must reject it", stmt)
		} else if !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%q failed with %v; the message should say the table is append-only", stmt, err)
		}
	}

	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("audit_log holds %d rows after the tampering attempts, want 1", n)
	}
}

func TestWorklogsAndMessagesCannotBeDeleted(t *testing.T) {
	db, ctx, f := migratedDB(t)

	mustExec(t, ctx, db, `INSERT INTO worklogs (id, project_id, actor_id, kind, body, created_at)
		VALUES ($1, $2, $3, 'progress', 'the auth library version is wrong', now())`,
		uuid.Must(uuid.NewV7()), f.ProjectID, f.ActorID)

	threadID := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, db, `INSERT INTO threads (id, project_id, scope_kind, created_at) VALUES ($1, $2, 'project', now())`,
		threadID, f.ProjectID)
	mustExec(t, ctx, db, `INSERT INTO messages (id, thread_id, actor_id, role, content, seq, created_at)
		VALUES ($1, $2, $3, 'user', 'hello', 1, now())`,
		uuid.Must(uuid.NewV7()), threadID, f.ActorID)

	for _, stmt := range []string{
		`DELETE FROM worklogs`,
		`DELETE FROM messages`,
	} {
		if _, err := db.Pool.Exec(ctx, stmt); err == nil {
			t.Errorf("%q succeeded; worklogs and messages are corrected by adding, never by deleting", stmt)
		}
	}
}

// TestAuditExportIndexesPresent is D13: the export indexes ship in the first
// migration. Adding them later means building an index on a table you are
// trying to read, at the moment you most want the data.
func TestAuditExportIndexesPresent(t *testing.T) {
	db, ctx, _ := migratedDB(t)

	for _, name := range []string{
		"audit_project_at", "audit_subject_at", "audit_actor_at",
		"audit_action_at", "audit_request",
	} {
		var exists bool
		err := db.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE tablename = 'audit_log' AND indexname = $1)`,
			name).Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("export index %s is missing from migration 0001", name)
		}
	}
}

// TestRequestIDIsRequired covers both halves: the column is NOT NULL, and the
// audit writer refuses before it gets there so the message names the cause
// rather than the constraint.
func TestRequestIDIsRequired(t *testing.T) {
	db, ctx, f := migratedDB(t)

	err := audit.Write(context.Background(), db.Pool, audit.Entry{
		ProjectID: &f.ProjectID,
		ActorID:   f.ActorID,
		Action:    audit.ActionProjectCreated,
	})
	if err == nil {
		t.Fatal("audit.Write accepted an entry with no request id in context")
	}
	if !strings.Contains(err.Error(), "request_id") {
		t.Errorf("error should name request_id: %v", err)
	}

	_, err = db.Pool.Exec(ctx,
		`INSERT INTO audit_log (id, project_id, actor_id, action, subject_kind, request_id, at)
		 VALUES ($1, $2, $3, 'project.created', 'project', NULL, now())`,
		uuid.Must(uuid.NewV7()), f.ProjectID, f.ActorID)
	if err == nil {
		t.Error("the database accepted a NULL request_id")
	}
}

func TestAuditWriteRejectsUnregisteredAction(t *testing.T) {
	db, ctx, f := migratedDB(t)

	err := audit.Write(ctx, db.Pool, audit.Entry{
		ProjectID: &f.ProjectID,
		ActorID:   f.ActorID,
		Action:    audit.Action("work_item.vibes_changed"),
	})
	if err == nil {
		t.Fatal("audit.Write accepted an action that is not in the taxonomy")
	}
	if !strings.Contains(err.Error(), "taxonomy") {
		t.Errorf("error should point at the taxonomy: %v", err)
	}
}

func TestAuditWriteDerivesSubjectKind(t *testing.T) {
	db, ctx, f := migratedDB(t)
	writeEntry(t, ctx, db, f, audit.ActionWorkItemStatusChanged)

	var kind, action string
	if err := db.Pool.QueryRow(ctx,
		`SELECT subject_kind, action FROM audit_log LIMIT 1`).Scan(&kind, &action); err != nil {
		t.Fatal(err)
	}
	if action != "work_item.status_changed" || kind != "work_item" {
		t.Errorf("got action=%q subject_kind=%q; subject_kind must be derived from the action", action, kind)
	}
}

func writeEntry(t *testing.T, ctx context.Context, db *store.DB, f fixtures, action audit.Action) {
	t.Helper()
	err := db.InTx(ctx, func(tx pgx.Tx) error {
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &f.ProjectID,
			ActorID:   f.ActorID,
			Action:    action,
			SubjectID: &f.ProjectID,
			Before:    map[string]any{"status": "todo"},
			After:     map[string]any{"status": "in_progress"},
		})
	})
	if err != nil {
		t.Fatalf("writing audit entry: %v", err)
	}
}

// ── schema shape ─────────────────────────────────────────────────────────────

// TestSoftDeleteColumnsPresent is CONTRACT §6.5 constraint 1, enumerated. A
// hard delete removes the row and orphans every audit entry pointing at it,
// which is the one failure that makes a retrospective unanswerable.
func TestSoftDeleteColumnsPresent(t *testing.T) {
	db, ctx, _ := migratedDB(t)

	for _, table := range []string{
		"milestones", "work_items", "sub_tasks", "checklist_items", "deviations",
		"catalog_entities", "diagram_nodes", "diagram_edges", "notes",
	} {
		var exists bool
		err := db.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.columns
			                WHERE table_name = $1 AND column_name = 'deleted_at')`,
			table).Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("%s has no deleted_at column; every object with a lifecycle is soft-deleted", table)
		}
	}
}

func TestEmbeddingDimMatchesTheContract(t *testing.T) {
	db, ctx, _ := migratedDB(t)

	dim, err := db.EmbeddingDim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dim != 1024 {
		t.Errorf("chunks.embedding is VECTOR(%d); the contract and the configured embedding model both say 1024", dim)
	}
}

func TestEmbeddingDimIsZeroBeforeMigration(t *testing.T) {
	db, ctx := freshDB(t)

	dim, err := db.EmbeddingDim(ctx)
	if err != nil {
		t.Fatalf("reading the dimension on an unmigrated database should not error: %v", err)
	}
	if dim != 0 {
		t.Errorf("got %d, want 0 on an unmigrated database", dim)
	}
}

// TestClosedVocabularyEnforced checks that the node kinds and edge relations
// are a schema constraint, not only an application convention. CONTRACT §3.3
// says a vocabulary change is a migration; this is what makes that true.
func TestClosedVocabularyEnforced(t *testing.T) {
	db, ctx, f := migratedDB(t)

	_, err := db.Pool.Exec(ctx,
		`INSERT INTO catalog_entities (id, project_id, kind, name, slug, provenance, actor_id, created_at, updated_at)
		 VALUES ($1, $2, 'microservice', 'Auth', 'auth', 'human_created', $3, now(), now())`,
		uuid.Must(uuid.NewV7()), f.ProjectID, f.ActorID)
	if err == nil {
		t.Error("the database accepted the node kind 'microservice', which is not in the closed vocabulary")
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	db, ctx, f := migratedDB(t)

	sentinel := errors.New("deliberate")
	err := db.InTx(ctx, func(tx pgx.Tx) error {
		if err := audit.Write(ctx, tx, audit.Entry{
			ProjectID: &f.ProjectID,
			ActorID:   f.ActorID,
			Action:    audit.ActionProjectCreated,
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx returned %v, want the sentinel", err)
	}

	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d audit rows survived a rolled back transaction; the write and its audit entry share one transaction", n)
	}
}

func TestMigrateDownThenUp(t *testing.T) {
	db, ctx, _ := migratedDB(t)

	if _, err := store.MigrateDown(ctx, db, quietLog()); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	v, err := store.SchemaVersion(ctx, db.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Errorf("schema version after down is %d, want 0", v)
	}

	if _, err := store.MigrateUp(ctx, db, quietLog()); err != nil {
		t.Fatalf("re-applying after down: %v", err)
	}
	if _, err := db.EmbeddingDim(ctx); err != nil {
		t.Fatalf("schema is not usable after down then up: %v", err)
	}
}
