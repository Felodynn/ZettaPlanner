package serialize_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The one structural rule (BACKEND.md §1.1), enforced here and never deleted.
//
// D5 says the semantic layer and the presentation layer are separate stores, and
// that the packages producing model context must not import the layout store.
// Not "we filter coordinates before the call" — a filter is a step somebody can
// forget, and the failure it produces is a large diagram whose geometry invites
// the planner to derive flow from position instead of from relations.
//
// This test walks the transitive import closure of internal/serialize and
// internal/contextbuild and fails if any package in it names a layout table in
// a string literal — which is the only way a table name reaches Postgres.
//
// Stated twice on purpose: once as "nothing reachable talks to those tables",
// which is the property, and once as "these specific packages are not imported",
// which is the form a reader checks first. A third test proves the detector is
// still live, because a boundary check that has quietly stopped matching is
// worse than none.

// layoutTables are the presentation stores. A package in the protected closure
// mentioning any of them fails the test.
var layoutTables = []string{"diagram_layout", "diagram_edge_layout"}

// protected are the packages the rule applies to. contextbuild arrives in
// Phase 5; it is listed now so the rule is already in force when it does, rather
// than being remembered afterwards.
var protected = []string{
	"github.com/Felodynn/ZettaPlanner/backend/internal/serialize",
	"github.com/Felodynn/ZettaPlanner/backend/internal/contextbuild",
}

func TestSemanticPackagesCannotReachTheLayoutStore(t *testing.T) {
	for _, pkg := range protected {
		deps, ok := transitiveDeps(t, pkg)
		if !ok {
			t.Logf("%s does not exist yet; the rule will apply when it does", pkg)
			continue
		}
		for _, dep := range deps {
			if table, line := mentionsLayout(t, dep.Dir); table != "" {
				t.Errorf(
					"%s can reach %s, which mentions %s:\n    %s\n\n"+
						"D5 makes this structural rather than a filtering step. Move whatever is needed into "+
						"internal/domain/diagram (which has no coordinate field), or take the dependency out.",
					pkg, dep.ImportPath, table, strings.TrimSpace(line))
			}
		}
	}
}

// TestTheBoundaryCheckActuallyDetects guards the guard.
//
// If the detector silently stopped matching — a renamed table, a broken walk —
// the test above would pass forever while enforcing nothing. internal/repo does
// read diagram_layout, on purpose, so it is a standing positive control that
// costs nothing to maintain.
func TestTheBoundaryCheckActuallyDetects(t *testing.T) {
	deps, ok := transitiveDeps(t, "github.com/Felodynn/ZettaPlanner/backend/internal/repo")
	if !ok {
		t.Fatal("internal/repo not found; the positive control is gone")
	}

	var found bool
	for _, dep := range deps {
		if table, _ := mentionsLayout(t, dep.Dir); table != "" {
			found = true
			break
		}
	}
	if !found {
		t.Error("the detector found no mention of the layout tables anywhere in internal/repo's closure. " +
			"Either the tables were renamed and layoutTables is stale, or the walk is broken — " +
			"either way the rule above is no longer being enforced.")
	}
}

// TestProtectedPackagesDoNotImportTheRepo is the same rule stated as an import
// check, which is the form a reader will look for first.
func TestProtectedPackagesDoNotImportTheRepo(t *testing.T) {
	banned := map[string]string{
		"github.com/Felodynn/ZettaPlanner/backend/internal/repo":          "it reads diagram_layout",
		"github.com/Felodynn/ZettaPlanner/backend/internal/domain/layout": "it is the presentation layer",
	}
	for _, pkg := range protected {
		deps, ok := transitiveDeps(t, pkg)
		if !ok {
			continue
		}
		for _, dep := range deps {
			if why, isBanned := banned[dep.ImportPath]; isBanned {
				t.Errorf("%s imports %s, and %s", pkg, dep.ImportPath, why)
			}
		}
	}
}

// ── plumbing ─────────────────────────────────────────────────────────────────

type dep struct {
	ImportPath string
	Dir        string
}

// transitiveDeps asks the toolchain for the closure, rather than parsing
// imports here. `go list -deps` is the authority on what a package can reach,
// including through build tags, and reimplementing it would be a second answer
// that disagrees under exactly the conditions nobody tests.
func transitiveDeps(t *testing.T, pkg string) ([]dep, bool) {
	t.Helper()

	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}\t{{.Dir}}", pkg)
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		// A package that does not exist yet is not a failure; one that fails to
		// build is.
		stderr := exitErrStderr(err)
		for _, absent := range []string{
			"no Go files",
			"cannot find package",
			"matched no packages",
			"no required module provides package",
		} {
			if strings.Contains(stderr, absent) {
				return nil, false
			}
		}
		t.Fatalf("go list -deps %s: %v\n%s", pkg, err, stderr)
	}

	const modulePrefix = "github.com/Felodynn/ZettaPlanner/backend/"
	var deps []dep
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		path, dir, ok := strings.Cut(line, "\t")
		if !ok || dir == "" {
			continue
		}
		// Only this module's packages. The standard library and third-party
		// dependencies cannot be reading our tables.
		if !strings.HasPrefix(path, modulePrefix) {
			continue
		}
		deps = append(deps, dep{ImportPath: path, Dir: dir})
	}
	return deps, len(deps) > 0
}

// mentionsLayout returns the first layout table named in a string literal in a
// package's non-test sources, with the position it appeared at.
//
// String literals specifically, not raw text. A table name only reaches Postgres
// through one, and scanning the whole file would flag the doc comment in
// yaml.go that states this very rule — a check that cannot survive its own
// documentation is a check nobody will keep.
func mentionsLayout(t *testing.T, dir string) (string, string) {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", dir, err)
	}

	var (
		foundTable string
		foundWhere string
	)
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				if foundTable != "" {
					return false
				}
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				for _, table := range layoutTables {
					if strings.Contains(lit.Value, table) {
						foundTable = table
						foundWhere = filepath.Base(name) + ":" + fset.Position(lit.Pos()).String()
						return false
					}
				}
				return true
			})
		}
	}
	return foundTable, foundWhere
}

func moduleRoot(t *testing.T) string {
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
			t.Fatal("could not find go.mod")
		}
		dir = parent
	}
}

func exitErrStderr(err error) string {
	if e, ok := err.(*exec.ExitError); ok {
		return string(e.Stderr)
	}
	return ""
}
