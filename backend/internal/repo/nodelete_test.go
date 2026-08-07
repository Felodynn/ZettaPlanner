package repo_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// softDeleted is CONTRACT §6.5 constraint 1, enumerated. Every object with a
// lifecycle carries deleted_at, and a row from one of these tables must never
// leave the database: a hard delete removes the row *and* orphans every audit
// entry pointing at it, which is the one failure that makes a retrospective
// unanswerable.
var softDeleted = map[string]bool{
	"users":            true,
	"projects":         true,
	"milestones":       true,
	"work_items":       true,
	"sub_tasks":        true,
	"checklist_items":  true,
	"deviations":       true,
	"catalog_entities": true,
	"diagram_nodes":    true,
	"diagram_edges":    true,
	"notes":            true,
	// Never deleted at all; correct by adding.
	"worklogs":  true,
	"messages":  true,
	"audit_log": true,
	"actors":    true,
}

var deleteStmt = regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+([a-zA-Z_][a-zA-Z0-9_.]*)`)

// TestNoHardDeletes is the Phase 1 exit criterion, as a test rather than as a
// convention.
//
// It scans the application source for DELETE FROM. A statement is permitted only
// when both hold:
//
//   - it carries the `zp:hard-delete-ok` marker, so the exception was written
//     deliberately rather than reached for, and
//   - its table is not one the contract says carries deleted_at.
//
// The marker is the point. A blanket ban gets silenced the first time somebody
// genuinely needs a removal; requiring an annotation makes each exception a
// decision that shows up in review, next to the reason.
func TestNoHardDeletes(t *testing.T) {
	root := repoRoot(t)

	// The migration runner and the .sql files are exempt: DROP and DELETE in a
	// schema definition are how a schema is defined.
	skipDirs := map[string]bool{
		filepath.Join(root, "internal", "store", "migrations"): true,
	}
	skipFiles := map[string]bool{
		filepath.Join(root, "internal", "store", "migrate.go"):      true,
		filepath.Join(root, "internal", "repo", "nodelete_test.go"): true,
		filepath.Join(root, "internal", "store", "store_test.go"):   true,
	}

	var violations []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipDirs[path] || info.Name() == ".git" || info.Name() == "bin" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || skipFiles[path] {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)

		for i, line := range strings.Split(string(body), "\n") {
			match := deleteStmt.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			table := strings.ToLower(match[1])

			if softDeleted[table] {
				violations = append(violations, fmt.Sprintf(
					"%s:%d deletes from %s, which the contract says is soft-deleted. Set deleted_at and write an audit entry.",
					rel, i+1, table))
				continue
			}
			if !strings.Contains(line, "zp:hard-delete-ok") {
				violations = append(violations, fmt.Sprintf(
					"%s:%d deletes from %s with no `zp:hard-delete-ok` marker. If the removal is genuinely correct, "+
						"annotate it and say why in a comment; if it is not, set deleted_at instead.",
					rel, i+1, table))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range violations {
		t.Error(v)
	}
}

// TestTheScanActuallyCatchesSomething guards the guard. A regex that silently
// stopped matching would leave the previous test passing forever while
// enforcing nothing.
func TestTheScanActuallyCatchesSomething(t *testing.T) {
	cases := []struct {
		line       string
		wantTable  string
		wantCaught bool
	}{
		{`const sql = "DELETE FROM work_items WHERE id = $1"`, "work_items", true},
		{`	q.Exec(ctx, "delete from worklogs")`, "worklogs", true},
		{`const sql = "DELETE FROM project_members WHERE x = $1" // zp:hard-delete-ok`, "project_members", true},
		{`// we never DELETE FROM anything`, "anything", true},
		{`	q.Exec(ctx, "UPDATE users SET deleted_at = now()")`, "", false},
	}
	for _, tc := range cases {
		match := deleteStmt.FindStringSubmatch(tc.line)
		if tc.wantCaught && match == nil {
			t.Errorf("the scan missed: %s", tc.line)
			continue
		}
		if !tc.wantCaught && match != nil {
			t.Errorf("the scan matched a line it should not have: %s", tc.line)
			continue
		}
		if tc.wantCaught && strings.ToLower(match[1]) != tc.wantTable {
			t.Errorf("extracted table %q, want %q from: %s", match[1], tc.wantTable, tc.line)
		}
	}
}

// repoRoot walks up to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod above the test's working directory")
		}
		dir = parent
	}
}
