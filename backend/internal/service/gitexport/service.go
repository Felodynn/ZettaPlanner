// Package gitexport builds and commits the one-way git projection (D10).
//
// **It never touches a write path.** There is no hook in the diagram service,
// no callback after a revision, no publish into the transaction that saved
// something. A worker wakes on a timer, asks the database what the project
// looks like now, writes that, and commits if anything moved. Which means the
// contract's "git failure never blocks or corrupts a DB write" is a structural
// property rather than a promise about error handling: there is no code path
// where a write is waiting for git to answer.
//
// The cost is staleness of up to one poll interval, and staleness is exactly
// what D10 says the projection is allowed to be.
//
// **It stores no state in the database.** No projected_revision column, no
// projections table. The tree either matches the database or it does not, and
// `git diff --cached` is the authority on which. A stored watermark would be a
// second source of truth about a thing whose entire purpose is to be
// disposable — and the moment it disagreed, the projection would go quiet with
// no way to notice. There is one local file inside `.git` recording how far the
// log was read, used only to decide whose name a commit carries; losing it
// costs one rebuild that git then declines to commit. Recorded as D62.
package gitexport

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/gitproj"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/serialize"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// projectedActions is the set of audit actions this projection carries.
//
// Used only to decide whether a commit is warranted and whose name it gets. A
// worklog is a real change to the project and is not in the tree, so counting
// it would attribute an architecture commit to whoever last wrote one.
var projectedActions = []string{
	string(audit.ActionEntityCreated), string(audit.ActionEntityUpdated), string(audit.ActionEntityDeleted),
	string(audit.ActionDiagramCreated), string(audit.ActionDiagramUpdated), string(audit.ActionDiagramDeleted),
	string(audit.ActionDiagramRevised),
	string(audit.ActionNodeCreated), string(audit.ActionNodeUpdated), string(audit.ActionNodeDeleted),
	string(audit.ActionEdgeCreated), string(audit.ActionEdgeUpdated), string(audit.ActionEdgeDeleted),
	string(audit.ActionIngestAccepted),
	string(audit.ActionProjectUpdated),
}

// Service projects projects into git.
type Service struct {
	db   *store.DB
	log  *slog.Logger
	root string
	poll time.Duration

	projects repo.Projects
	diagrams repo.Diagrams
	entities repo.Entities
	logs     repo.AuditLogs

	// one sync at a time per process. Two concurrent syncs of the same project
	// would race on a working tree that one of them is in the middle of
	// deleting.
	mu   sync.Mutex
	stop chan struct{}
	done chan struct{}
}

// New builds the service. root is the parent directory holding one repository
// per project.
func New(db *store.DB, log *slog.Logger, root string, poll time.Duration) *Service {
	if log == nil {
		log = slog.Default()
	}
	if poll <= 0 {
		poll = time.Minute
	}
	return &Service{db: db, log: log, root: root, poll: poll}
}

// Report is what one sync did.
type Report struct {
	ProjectKey string
	Dir        string
	Changed    bool
	Commit     string
	Tree       string
	Files      int
	Message    string
	Identity   gitproj.Identity
	// Skipped is set when the projection is off for this project.
	Skipped bool
}

// ── syncing ──────────────────────────────────────────────────────────────────

// Sync projects one project. force ignores the "nothing new in the log" check
// and rebuilds the tree regardless, which is what `resync` does.
func (s *Service) Sync(ctx context.Context, projectID uuid.UUID, force bool) (*Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, err := s.projects.ByID(ctx, s.db.Pool, projectID)
	if err != nil {
		return nil, err
	}
	if !project.GitEnabled && !force {
		return &Report{ProjectKey: project.Key, Skipped: true}, nil
	}

	dir := s.DirFor(project.Key)
	gr, err := gitproj.Open(ctx, dir)
	if err != nil {
		return nil, err
	}

	watermark, hadWatermark := gr.Watermark()
	summary, err := s.logs.SummarySince(ctx, s.db.Pool, projectID, watermark, projectedActions)
	if err != nil {
		return nil, err
	}
	if summary.Total == 0 && hadWatermark && !force {
		// Nothing the projection carries has happened. Rebuilding the tree
		// would produce the same bytes and git would decline the commit; this
		// only skips the work.
		return &Report{ProjectKey: project.Key, Dir: dir}, nil
	}

	files, err := s.Tree(ctx, projectID)
	if err != nil {
		return nil, err
	}

	identity := identityFor(summary.Actors)
	message := commitMessage(project.Key, summary, force || !hadWatermark)

	res, err := gr.Sync(ctx, files, message, identity)
	if err != nil {
		return nil, err
	}

	// The watermark moves only after a successful commit. Moving it first would
	// mean a failed sync silently forgets what it had not yet projected.
	if summary.MaxID != uuid.Nil {
		if err := gr.SetWatermark(summary.MaxID); err != nil {
			s.log.WarnContext(ctx, "the projection committed but its watermark could not be written; "+
				"the next sync will rebuild the same tree and commit nothing",
				"project", project.Key, "error", err)
		}
	} else if !hadWatermark {
		if latest, ok, err := s.logs.MaxID(ctx, s.db.Pool, projectID); err == nil && ok {
			_ = gr.SetWatermark(latest)
		}
	}

	return &Report{
		ProjectKey: project.Key, Dir: dir, Changed: res.Changed, Commit: res.Commit,
		Tree: res.Tree, Files: res.Files, Message: message, Identity: identity,
	}, nil
}

// SyncAll projects every project with the projection enabled.
func (s *Service) SyncAll(ctx context.Context) []*Report {
	ids, err := s.enabled(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "could not list the projects with a git projection", "error", err)
		return nil
	}
	var out []*Report
	for _, id := range ids {
		rep, err := s.Sync(ctx, id, false)
		if err != nil {
			// Logged and stepped over. One project with an unwritable directory
			// must not stop the others, and it certainly must not stop the
			// process — nothing upstream depends on this succeeding.
			s.log.ErrorContext(ctx, "git projection failed; the database is unaffected",
				"project_id", id, "error", err)
			continue
		}
		out = append(out, rep)
	}
	return out
}

// Start runs the projection worker until Stop.
func (s *Service) Start(ctx context.Context) {
	if s.stop != nil {
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})

	go func() {
		defer close(s.done)
		ticker := time.NewTicker(s.poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-ticker.C:
				s.SyncAll(ctx)
			}
		}
	}()
}

// Stop ends the worker and waits for an in-flight sync to finish.
//
// Waits rather than cancels: a sync interrupted between deleting the working
// tree and committing leaves a directory that looks like a projection of
// nothing, and somebody would have to know to run resync to find out otherwise.
func (s *Service) Stop() {
	if s.stop == nil {
		return
	}
	close(s.stop)
	<-s.done
	s.stop, s.done = nil, nil
}

// DirFor is where a project's repository lives.
func (s *Service) DirFor(projectKey string) string {
	return filepath.Join(s.root, strings.ToUpper(projectKey))
}

func (s *Service) enabled(ctx context.Context) ([]uuid.UUID, error) {
	const sql = `SELECT id FROM projects WHERE git_enabled AND deleted_at IS NULL ORDER BY key`
	rows, err := s.db.Pool.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ── the tree ─────────────────────────────────────────────────────────────────

// Tree builds the complete file set for a project.
//
// Pure with respect to the database: given the same rows it produces the same
// bytes, which is what makes "delete the directory, resync, get an identical
// tree" a property rather than a hope. The one input that is not a row is the
// project key, and it is in the path, not in the content.
func (s *Service) Tree(ctx context.Context, projectID uuid.UUID) (map[string][]byte, error) {
	q := s.db.Pool

	project, err := s.projects.ByID(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	diagrams, err := s.diagrams.ListByProject(ctx, q, projectID)
	if err != nil {
		return nil, err
	}

	files := map[string][]byte{}
	index := serialize.ProjectFile{
		Key: project.Key, Name: project.Name, Description: project.Description,
	}

	// placements is which diagrams each entity is drawn in, collected while the
	// graphs are already loaded rather than by a second query per entity.
	placements := map[uuid.UUID][]string{}

	for _, d := range diagrams {
		graph, err := s.diagrams.LoadGraph(ctx, q, d.ID)
		if err != nil {
			return nil, err
		}
		index.Diagrams = append(index.Diagrams, serialize.DiagramRef{
			Slug: d.Slug, Name: d.Name, Level: d.Level, Revision: d.CurrentRevision,
		})

		dir := serialize.DiagramDir(d.Slug)
		meta, err := serialize.DiagramMeta(d)
		if err != nil {
			return nil, err
		}
		files[dir+"/diagram.yaml"] = meta

		for _, n := range graph.Nodes {
			if n.DeletedAt != nil || n.Entity == nil {
				continue
			}
			body, err := serialize.Node(n)
			if err != nil {
				return nil, err
			}
			files[dir+"/"+serialize.NodePath(n)] = body
			placements[n.EntityID] = append(placements[n.EntityID], d.Slug)
		}

		for _, e := range graph.Edges {
			if e.DeletedAt != nil {
				continue
			}
			from, to := graph.NodeByID(e.FromNodeID), graph.NodeByID(e.ToNodeID)
			if from == nil || to == nil || from.DeletedAt != nil || to.DeletedAt != nil {
				continue
			}
			body, err := serialize.Edge(graph, e)
			if err != nil {
				return nil, err
			}
			files[dir+"/"+serialize.EdgePath(from, to, e.Relation)] = body
		}
	}

	entities, err := s.entities.ListByProject(ctx, q, projectID)
	if err != nil {
		return nil, err
	}
	for _, e := range entities {
		body, err := serialize.Entity(e, placements[e.ID])
		if err != nil {
			return nil, err
		}
		files[serialize.EntityPath(e.Slug)] = body
	}

	root, err := serialize.Project(index)
	if err != nil {
		return nil, err
	}
	files["project.yaml"] = root
	files["README.md"] = readme(project.Key, project.Name)
	return files, nil
}

func readme(key, name string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — %s\n\n", key, name)
	b.WriteString("A **one-way projection** of a ZettaPlanner project's architecture.\n\n")
	b.WriteString("Postgres is the store of record (D10). This tree is generated from it, overwritten in " +
		"full on every sync, and read back by nothing. An edit made here reaches nobody and disappears " +
		"the next time anything upstream changes.\n\n")
	b.WriteString("It is allowed to be stale, and it is disposable: delete the directory, run " +
		"`zettaplanner git resync`, and the result is byte-identical.\n\n")
	b.WriteString("## Layout\n\n")
	b.WriteString("- `project.yaml` — the project and its diagrams\n")
	b.WriteString("- `entities/<slug>.yaml` — one file per catalog entity, the identity behind every placement\n")
	b.WriteString("- `diagrams/<slug>/diagram.yaml` — what the picture is\n")
	b.WriteString("- `diagrams/<slug>/nodes/<entity-slug>.yaml` — one file per placement\n")
	b.WriteString("- `diagrams/<slug>/edges/<from>__<relation>__<to>.yaml` — one file per relation\n\n")
	b.WriteString("Paths are derived from slugs and never from display names, so a rename is a rename " +
		"rather than a delete plus an add. There are no coordinates anywhere: the serializer's only " +
		"input type has no coordinate field (D5).\n")
	return []byte(b.String())
}

// ── attribution ──────────────────────────────────────────────────────────────

// identityFor decides whose name the commit carries.
//
// One actor gets their own identity. More than one gets the harness, because
// attributing a batch to whoever happened to be in it last puts somebody's name
// on a change they did not make — and `git log --author` is the cheapest answer
// to "what did the model change", which only works if that never happens.
func identityFor(actors []repo.AuditActor) gitproj.Identity {
	if len(actors) != 1 {
		return gitproj.HarnessIdentity
	}
	a := actors[0]
	switch a.Kind {
	case "human":
		email := "unknown@zettaplanner.invalid"
		if a.Name != "" {
			email = strings.ToLower(strings.ReplaceAll(a.Name, " ", ".")) + "@zettaplanner.invalid"
		}
		return gitproj.Identity{Name: a.Name, Email: email}
	case "agent":
		return gitproj.AgentIdentity(a.Name, a.ModelRef)
	default:
		return gitproj.HarnessIdentity
	}
}

// commitMessage says what the commit carries, in the terms the audit log
// records — so `git log` and the audit query answer the same question the same
// way rather than being two accounts of one afternoon.
func commitMessage(key string, summary repo.AuditSummary, full bool) string {
	var b strings.Builder
	switch {
	case full:
		fmt.Fprintf(&b, "%s: full resync", key)
	case summary.Total == 1:
		fmt.Fprintf(&b, "%s: 1 architecture change", key)
	default:
		fmt.Fprintf(&b, "%s: %d architecture changes", key, summary.Total)
	}

	if len(summary.Counts) > 0 {
		actions := make([]string, 0, len(summary.Counts))
		for a := range summary.Counts {
			actions = append(actions, a)
		}
		sort.Strings(actions)
		parts := make([]string, 0, len(actions))
		for _, a := range actions {
			if n := summary.Counts[a]; n > 1 {
				parts = append(parts, fmt.Sprintf("%s ×%d", a, n))
			} else {
				parts = append(parts, a)
			}
		}
		b.WriteString("\n\n" + strings.Join(parts, ", ") + "\n")
	}
	if len(summary.Actors) > 0 {
		names := make([]string, 0, len(summary.Actors))
		for _, a := range summary.Actors {
			if a.Kind == "agent" && a.ModelRef != "" {
				names = append(names, fmt.Sprintf("%s (agent, %s)", a.Name, a.ModelRef))
				continue
			}
			names = append(names, fmt.Sprintf("%s (%s)", a.Name, a.Kind))
		}
		if len(summary.Counts) == 0 {
			b.WriteString("\n")
		}
		b.WriteString("\nActors: " + strings.Join(names, ", ") + "\n")
	}
	return b.String()
}
