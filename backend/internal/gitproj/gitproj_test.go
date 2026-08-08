package gitproj_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/gitproj"
)

func requireGit(t *testing.T) {
	t.Helper()
	if err := gitproj.Available(); err != nil {
		t.Skip("git is not on PATH; skipping the projection tests")
	}
}

func tree() map[string][]byte {
	return map[string][]byte{
		"project.yaml":                        []byte("project: ZP\nname: ZettaPlanner\n"),
		"entities/auth-service.yaml":          []byte("id: auth-service\nkind: service\n"),
		"diagrams/ctx/nodes/token-store.yaml": []byte("id: token-store\nkind: datastore\n"),
	}
}

func TestASecondSyncOfTheSameStateCommitsNothing(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	r, err := gitproj.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	first, err := r.Sync(ctx, tree(), "ZP: first", gitproj.HarnessIdentity)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if !first.Changed {
		t.Fatal("the first sync of a fresh repository committed nothing")
	}

	second, err := r.Sync(ctx, tree(), "ZP: again", gitproj.HarnessIdentity)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if second.Changed {
		// A projection that commits on every poll makes `git log` useless and
		// the repository grow without anything having happened.
		t.Fatal("the same database state produced a second commit")
	}
	if second.Tree != first.Tree {
		t.Fatalf("tree moved without a commit: %s then %s", first.Tree, second.Tree)
	}
}

func TestRebuildingFromScratchProducesAnIdenticalTree(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	dir := t.TempDir()
	r, err := gitproj.Open(ctx, dir)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	before, err := r.Sync(ctx, tree(), "ZP: first", gitproj.HarnessIdentity)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// The whole safety argument for the projection is that it is disposable.
	// Delete it entirely and rebuild.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			t.Fatalf("clearing: %v", err)
		}
	}

	rebuilt, err := gitproj.Open(ctx, dir)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	after, err := rebuilt.Sync(ctx, tree(), "ZP: full resync", gitproj.HarnessIdentity)
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	if after.Tree != before.Tree {
		t.Fatalf("a rebuild from scratch produced a different tree:\n  before %s\n  after  %s",
			before.Tree, after.Tree)
	}
	if after.Commit == before.Commit {
		// Different commits with the same tree is the correct outcome: a commit
		// carries a time and an author, and the content is the tree.
		t.Fatal("the rebuilt repository somehow has the same commit hash")
	}
}

func TestSyncRemovesWhatIsNoLongerInTheDatabase(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	dir := t.TempDir()
	r, err := gitproj.Open(ctx, dir)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if _, err := r.Sync(ctx, tree(), "ZP: first", gitproj.HarnessIdentity); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	shrunk := tree()
	delete(shrunk, "entities/auth-service.yaml")
	if _, err := r.Sync(ctx, shrunk, "ZP: one deleted", gitproj.HarnessIdentity); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "entities/auth-service.yaml")); err == nil {
		// An additive projection keeps a file for everything anybody ever
		// deleted, and every diff still looks clean while the tree slowly
		// becomes a different graph from the database.
		t.Fatal("a deleted entity is still in the projected tree")
	}
}

func TestItRefusesADirectoryItDidNotMake(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	_, err := gitproj.Open(ctx, dir)
	if err == nil {
		// Sync deletes everything outside .git, and the path comes from
		// configuration. One wrong ZP_GIT_DIR is somebody's source tree.
		t.Fatal("opened a directory with unrelated content in it; a sync would have deleted the file")
	}
	if !strings.Contains(err.Error(), gitproj.MarkerPath) {
		t.Errorf("the refusal does not name what makes a directory safe:\n%v", err)
	}
}

func TestTheMarkerMakesTheDirectoryReusable(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	dir := t.TempDir()
	r, err := gitproj.Open(ctx, dir)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if _, err := r.Sync(ctx, tree(), "ZP: first", gitproj.HarnessIdentity); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := gitproj.Open(ctx, dir); err != nil {
		t.Fatalf("reopening a directory this package produced: %v", err)
	}
}

func TestTheWatermarkSurvivesButIsNotInTheTree(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	dir := t.TempDir()
	r, err := gitproj.Open(ctx, dir)
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if _, err := r.Sync(ctx, tree(), "ZP: first", gitproj.HarnessIdentity); err != nil {
		t.Fatalf("sync: %v", err)
	}

	mark := uuid.Must(uuid.NewV7())
	if err := r.SetWatermark(mark); err != nil {
		t.Fatalf("writing the watermark: %v", err)
	}
	got, ok := r.Watermark()
	if !ok || got != mark {
		t.Fatalf("watermark = %v (%v), want %v", got, ok, mark)
	}

	// In the tree it would produce a commit every time anything at all was
	// audited, including changes this projection does not carry.
	before, err := r.TreeHash(ctx)
	if err != nil {
		t.Fatalf("tree hash: %v", err)
	}
	res, err := r.Sync(ctx, tree(), "ZP: nothing", gitproj.HarnessIdentity)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Changed || res.Tree != before {
		t.Fatalf("the watermark leaked into the tree: changed=%v %s → %s", res.Changed, before, res.Tree)
	}
}

func TestHumanAndAgentCommitsCarryDistinctIdentities(t *testing.T) {
	requireGit(t)
	ctx := context.Background()

	r, err := gitproj.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	if _, err := r.Sync(ctx, tree(), "ZP: by a person",
		gitproj.Identity{Name: "Sunil", Email: "sunil@zettaplanner.invalid"}); err != nil {
		t.Fatalf("human sync: %v", err)
	}

	agentTree := tree()
	agentTree["entities/token-store.yaml"] = []byte("id: token-store\nkind: datastore\n")
	if _, err := r.Sync(ctx, agentTree, "ZP: by the planner",
		gitproj.AgentIdentity("planner", "glm-5.2:cloud")); err != nil {
		t.Fatalf("agent sync: %v", err)
	}

	lines, err := r.Log(ctx, 10)
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	joined := strings.Join(lines, "\n")
	// `git log --author` is the cheapest possible answer to "what did the model
	// change", and it only works if the two were never merged into one name.
	if !strings.Contains(joined, "Sunil") || !strings.Contains(joined, "planner (glm-5.2:cloud)") {
		t.Fatalf("the two authors are not distinguishable in the log:\n%s", joined)
	}
}
