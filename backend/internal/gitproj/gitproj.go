// Package gitproj is the git plumbing under the one-way projection (D10).
//
// **Postgres is the store of record and git is a projection.** It is allowed to
// be stale, it is regenerable from the database at any moment, and nothing ever
// reads it back in. That is what makes it safe: both-as-truth is the dual-write
// failure D1 rejected, and a projection that becomes an input is a correctness
// bug rather than a feature.
//
// Three consequences shape this package:
//
//  1. **Every sync writes the whole tree.** Not a diff, not a patch — the
//     complete file set, with everything else removed first. Incremental
//     projection means a delete that got missed leaves a file behind forever,
//     and the divergence is invisible because nothing compares.
//
//  2. **It shells out to git rather than linking a git library.** The output
//     has to be a repository a person opens in their own tools, and the
//     authority on what that means is the binary they already have. The
//     environment is scrubbed on every call so a developer's global config —
//     hooks, templates, gpgsign, autocrlf — cannot change what lands.
//
//  3. **It refuses to touch a directory it did not make.** The sync deletes
//     everything not under .git, and the path comes from configuration, so one
//     wrong ZP_GIT_DIR is somebody's source tree. A directory qualifies only if
//     it is empty or already carries the marker this package writes.
package gitproj

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// MarkerPath is written into every projected tree. Its presence is what makes
// a directory safe to wipe, and its content is fixed so it never produces a
// diff of its own.
const MarkerPath = ".zettaplanner/PROJECTION.md"

// Marker is the marker file's content.
const Marker = `# This directory is a projection

Every file here is generated from a ZettaPlanner database and is **overwritten
in full** on the next sync. Nothing reads it back in: an edit made here reaches
nobody, and will be gone the moment anything changes upstream.

Postgres is the store of record. If this tree is lost, delete it and re-run
` + "`zettaplanner git resync`" + ` — the result is byte-identical.
`

// watermarkFile lives inside .git, which is deliberate: it is local bookkeeping
// about *this* clone, not projected content. In the tree it would produce a
// commit every time anything at all was audited, including changes this
// projection does not carry — a stream of commits whose diff is one number.
const watermarkFile = "zettaplanner-watermark"

// Identity is who a commit is attributed to.
//
// Human and agent commits use distinct identities (§6.2): `git log --author` is
// the cheapest possible answer to "what did the model change", and it only
// works if the two were never merged into one signature.
type Identity struct {
	Name  string
	Email string
}

// HarnessIdentity is used when a sync covers changes by more than one actor, or
// by none this process can name. Deliberately not a person: attributing a mixed
// batch to whoever happened to be last would put somebody's name on a change
// they did not make.
var HarnessIdentity = Identity{Name: "ZettaPlanner", Email: "projection@zettaplanner.invalid"}

// AgentIdentity builds the identity for an agent actor.
func AgentIdentity(agentName, modelRef string) Identity {
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = "agent"
	}
	if modelRef != "" {
		name += " (" + modelRef + ")"
	}
	return Identity{Name: name, Email: "agent@zettaplanner.invalid"}
}

// Available reports whether git can be run at all.
func Available() error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("the git projection needs the `git` binary on PATH and it is not there: %w. "+
			"Install git, or leave the projection off — nothing else depends on it", err)
	}
	return nil
}

// Repo is one project's projected repository.
type Repo struct {
	dir string
}

// Open prepares dir as a projection repository, creating and initialising it if
// necessary.
//
// Refuses a directory that has content but no marker. The alternative is a
// single mistyped path silently deleting a working tree, and no amount of
// documentation makes that recoverable.
func Open(ctx context.Context, dir string) (*Repo, error) {
	if err := Available(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(abs, 0o750); err != nil {
			return nil, fmt.Errorf("creating the projection directory %s: %w", abs, err)
		}
	case err != nil:
		return nil, err
	case !info.IsDir():
		return nil, fmt.Errorf("%s is a file, not a directory; ZP_GIT_DIR names the parent of one "+
			"directory per project", abs)
	}

	r := &Repo{dir: abs}
	if err := r.checkOwnership(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := r.git(ctx, Identity{}, "init", "-q", "-b", "main"); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return r, nil
}

// Dir is the repository's absolute path.
func (r *Repo) Dir() string { return r.dir }

// checkOwnership refuses a directory this package did not produce.
func (r *Repo) checkOwnership() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return err
	}
	occupied := false
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		occupied = true
		break
	}
	if !occupied {
		return nil
	}
	if _, err := os.Stat(filepath.Join(r.dir, MarkerPath)); err == nil {
		return nil
	}
	return fmt.Errorf("%s already has content and is not a ZettaPlanner projection: it has no %s. "+
		"Refusing to touch it, because a sync deletes everything in the directory that is not under .git. "+
		"Point ZP_GIT_DIR somewhere else, or empty this directory yourself if it really is disposable",
		r.dir, MarkerPath)
}

// Result is what one sync did.
type Result struct {
	Changed bool
	Commit  string
	Tree    string
	Files   int
}

// Sync writes the whole tree, then commits if anything moved.
//
// The marker is added here rather than by the caller: it is what makes the next
// call safe, so it cannot be something a tree builder can forget.
func (r *Repo) Sync(ctx context.Context, files map[string][]byte, message string, id Identity) (Result, error) {
	if id.Name == "" || id.Email == "" {
		id = HarnessIdentity
	}

	tree := make(map[string][]byte, len(files)+1)
	for path, body := range files {
		clean, err := safeRelPath(path)
		if err != nil {
			return Result{}, err
		}
		tree[clean] = body
	}
	tree[MarkerPath] = []byte(Marker)

	if err := r.replaceWorkingTree(tree); err != nil {
		return Result{}, err
	}
	if _, err := r.git(ctx, id, "add", "-A"); err != nil {
		return Result{}, err
	}

	// `diff --cached --quiet` exits 1 when the index differs from HEAD. That
	// is the whole staleness check: no timestamp comparison, no stored
	// revision, no way for the answer to be wrong.
	staged, err := r.dirty(ctx)
	if err != nil {
		return Result{}, err
	}
	out := Result{Files: len(tree)}
	if !staged {
		out.Commit, _ = r.head(ctx)
		out.Tree, _ = r.TreeHash(ctx)
		return out, nil
	}

	if _, err := r.git(ctx, id, "commit", "-q", "--no-verify", "-m", message); err != nil {
		return Result{}, err
	}
	out.Changed = true
	if out.Commit, err = r.head(ctx); err != nil {
		return Result{}, err
	}
	if out.Tree, err = r.TreeHash(ctx); err != nil {
		return Result{}, err
	}
	return out, nil
}

// replaceWorkingTree deletes everything outside .git and writes the given files.
//
// Wholesale rather than incrementally: a projection that only adds and updates
// keeps a file for every node anybody ever deleted, and the tree slowly becomes
// a different graph from the one in the database while every diff looks clean.
func (r *Repo) replaceWorkingTree(files map[string][]byte) error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.dir, e.Name())); err != nil {
			return err
		}
	}

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		full := filepath.Join(r.dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(full, files[p], 0o640); err != nil {
			return err
		}
	}
	return nil
}

// TreeHash is the hash of HEAD's tree.
//
// The tree rather than the commit: two projections of the same database state
// have different commit hashes, because a commit carries a timestamp and an
// author. The tree is the content, and it is the tree that has to be identical
// after the directory is deleted and rebuilt from scratch.
func (r *Repo) TreeHash(ctx context.Context) (string, error) {
	out, err := r.git(ctx, Identity{}, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Log returns the most recent commits, one line each.
func (r *Repo) Log(ctx context.Context, limit int) ([]string, error) {
	out, err := r.git(ctx, Identity{}, "log", "--format=%h %an — %s", fmt.Sprintf("-%d", limit))
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

func (r *Repo) head(ctx context.Context) (string, error) {
	out, err := r.git(ctx, Identity{}, "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(out), err
}

func (r *Repo) dirty(ctx context.Context) (bool, error) {
	cmd := r.command(ctx, Identity{}, "diff", "--cached", "--quiet")
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("checking whether the projection has changes: %w", err)
}

// ── watermark ────────────────────────────────────────────────────────────────

// Watermark is the audit entry this repository was last projected through.
//
// Kept in .git and never committed. Its worst failure is being lost, and the
// consequence of that is one sync that rebuilds a tree identical to the one
// already there — which git then declines to commit.
func (r *Repo) Watermark() (uuid.UUID, bool) {
	body, err := os.ReadFile(filepath.Join(r.dir, ".git", watermarkFile))
	if err != nil {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(strings.TrimSpace(string(body)))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// SetWatermark records the audit entry this repository has now caught up to.
func (r *Repo) SetWatermark(id uuid.UUID) error {
	if id == uuid.Nil {
		return nil
	}
	return os.WriteFile(filepath.Join(r.dir, ".git", watermarkFile), []byte(id.String()+"\n"), 0o640)
}

// ── running git ──────────────────────────────────────────────────────────────

func (r *Repo) git(ctx context.Context, id Identity, args ...string) (string, error) {
	cmd := r.command(ctx, id, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s in %s: %w: %s",
			strings.Join(args, " "), r.dir, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// command builds a git invocation with the ambient environment scrubbed.
//
// A developer's global gitconfig can sign commits, run hooks from a template
// directory, rewrite line endings, or set a completely different identity. Any
// of those makes the projected tree depend on whose laptop it was generated on,
// which is the opposite of the determinism the whole serializer exists for.
func (r *Repo) command(ctx context.Context, id Identity, args ...string) *exec.Cmd {
	full := []string{
		"-C", r.dir,
		"-c", "core.autocrlf=false",
		"-c", "core.fileMode=false",
		"-c", "commit.gpgsign=false",
		"-c", "gc.auto=0",
	}
	if id.Name != "" && id.Email != "" {
		full = append(full,
			"-c", "user.name="+id.Name,
			"-c", "user.email="+id.Email,
		)
	}
	cmd := exec.CommandContext(ctx, "git", append(full, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	return cmd
}

// safeRelPath rejects anything that would write outside the repository.
func safeRelPath(p string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean("/" + strings.TrimPrefix(p, "/")))[1:]
	if clean == "" || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, ".git/") || clean == ".git" {
		return "", fmt.Errorf("the projection refused to write %q: a generated path escaped the tree, "+
			"which means a slug got into it that should have been slugified", p)
	}
	return clean, nil
}
