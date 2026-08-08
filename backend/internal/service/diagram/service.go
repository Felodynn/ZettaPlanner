// Package diagram orchestrates the architecture graph: entities, diagrams,
// leases, atomic saves, and the presentation layer.
//
// It reads both stores — it has to, because GET /diagrams/{id} returns semantic
// state *and* layout for the canvas. What it never does is hand a coordinate to
// anything that builds model context: the serializer takes a diagram.Graph,
// which has no coordinate field, and internal/serialize cannot import this
// package or the repo (D5, enforced by internal/serialize/boundary_test.go).
package diagram

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/layout"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/serialize"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// DefaultLeaseTTL is how long an edit session survives without a heartbeat.
//
// Q6, decided here and due to be revisited in dogfood. Five minutes with a
// 60-second heartbeat: long enough to survive a screen lock, a network blip, or
// a laptop lid closed while somebody answers the door; short enough that a
// browser killed mid-edit does not hold a diagram for an afternoon. A sleeping
// laptop loses its lease, which is correct — the alternative is a diagram
// nobody else can touch because of a machine that is not coming back.
const DefaultLeaseTTL = 5 * time.Minute

// HeartbeatInterval is what the client should use. Published in the acquire
// response so the frontend does not hard-code a number that later disagrees
// with the server's TTL.
const HeartbeatInterval = 60 * time.Second

// Service is the diagram application service.
type Service struct {
	db  *store.DB
	log *slog.Logger
	ttl time.Duration

	diagrams repo.Diagrams
	entities repo.Entities
	layouts  repo.Layouts
	leases   repo.Leases
	notes    repo.Notes
	graphs   repo.Graphs
	members  repo.Members
}

// New builds the service.
func New(db *store.DB, log *slog.Logger, leaseTTL time.Duration) *Service {
	if log == nil {
		log = slog.Default()
	}
	if leaseTTL <= 0 {
		leaseTTL = DefaultLeaseTTL
	}
	return &Service{db: db, log: log, ttl: leaseTTL}
}

// LeaseTTL is the configured edit-session lifetime.
func (s *Service) LeaseTTL() time.Duration { return s.ttl }

// subject resolves the caller's standing on a project.
func (s *Service) subject(ctx context.Context, q store.Querier, p policy.Principal, projectID uuid.UUID) (policy.Subject, error) {
	override, err := s.members.RoleFor(ctx, q, projectID, p.UserID)
	if err != nil {
		return policy.Subject{}, err
	}
	return policy.Subject{
		Principal:     p,
		ProjectID:     projectID,
		EffectiveRole: policy.Effective(p.GlobalRole, override),
		IsMember:      override != nil,
	}, nil
}

// requireMember rejects a caller who cannot see the project at all. Returns
// not_found rather than forbidden, for the same reason project reads do:
// distinguishing them enumerates what exists.
func (s *Service) requireMember(ctx context.Context, q store.Querier, p policy.Principal, projectID uuid.UUID) (policy.Subject, error) {
	subject, err := s.subject(ctx, q, p, projectID)
	if err != nil {
		return subject, err
	}
	if !subject.IsMember && p.GlobalRole != policy.RoleAdmin {
		return subject, fmt.Errorf("%w: diagram", domain.ErrNotFound)
	}
	return subject, nil
}

// ── diagrams ─────────────────────────────────────────────────────────────────

// CreateInput describes a new diagram.
type CreateInput struct {
	// ProjectID is set by Create from its own argument. It is a field so that
	// ApplyInput can carry a whole creation as one value.
	ProjectID uuid.UUID
	Name      string
	Slug      string
	Level     dg.Level
}

// Create makes an empty diagram.
func (s *Service) Create(ctx context.Context, projectID uuid.UUID, in CreateInput, p policy.Principal) (*dg.Diagram, error) {
	in.ProjectID = projectID
	if err := dg.ValidateLevel(in.Level); err != nil {
		return nil, err
	}
	slug := in.Slug
	if slug == "" {
		slug = dg.Slugify(in.Name)
	}
	if slug == "" {
		return nil, fmt.Errorf("%w: the diagram needs a name that produces a slug", domain.ErrInvalid)
	}

	now := time.Now()
	d := &dg.Diagram{
		ID: uuid.Must(uuid.NewV7()), ProjectID: projectID,
		Name: in.Name, Slug: slug, Level: in.Level,
		LayoutMode: dg.LayoutAuto, CreatedAt: now, UpdatedAt: now,
	}

	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		subject, err := s.requireMember(ctx, tx, p, projectID)
		if err != nil {
			return err
		}
		if den := policy.CheckCreate(policy.KindDiagram, subject); den != nil {
			return den
		}
		if err := s.diagrams.Create(ctx, tx, d); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &projectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionDiagramCreated, SubjectID: &d.ID,
			After: map[string]any{"slug": d.Slug, "name": d.Name, "level": string(d.Level)},
		})
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// List returns a project's diagrams.
// BySlug resolves a diagram by its stable slug within a project.
//
// The slug rather than the name, because the slug is the identity that does not
// follow a rename (D33) — an agent that learned "auth-context" from a serialized
// document must still find it after somebody retitles the diagram.
func (s *Service) BySlug(ctx context.Context, projectID uuid.UUID, slug string, p policy.Principal) (*dg.Diagram, error) {
	if _, err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.diagrams.BySlug(ctx, s.db.Pool, projectID, slug)
}

func (s *Service) List(ctx context.Context, projectID uuid.UUID, p policy.Principal) ([]*dg.Diagram, error) {
	if _, err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.diagrams.ListByProject(ctx, s.db.Pool, projectID)
}

// View is a diagram as the canvas needs it: semantic state, presentation state,
// the current revision, and whoever holds the lease.
//
// The two layers are assembled here, at the API boundary, and nowhere deeper.
type View struct {
	Graph     *dg.Graph
	Layout    map[uuid.UUID]*layout.Placement
	EdgeRoute map[uuid.UUID][]layout.Point
	Lease     *repo.Lease
}

// Get returns a diagram with both layers.
func (s *Service) Get(ctx context.Context, diagramID uuid.UUID, p policy.Principal) (*View, error) {
	d, err := s.diagrams.ByID(ctx, s.db.Pool, diagramID)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireMember(ctx, s.db.Pool, p, d.ProjectID); err != nil {
		return nil, err
	}

	graph, err := s.diagrams.LoadGraph(ctx, s.db.Pool, diagramID)
	if err != nil {
		return nil, err
	}
	placements, err := s.layouts.Get(ctx, s.db.Pool, diagramID)
	if err != nil {
		return nil, err
	}
	routes, err := s.layouts.EdgeRoutes(ctx, s.db.Pool, diagramID)
	if err != nil {
		return nil, err
	}
	lease, err := s.leases.Get(ctx, s.db.Pool, diagramID)
	if err != nil {
		return nil, err
	}
	if !lease.Live(time.Now()) {
		lease = nil
	}
	return &View{Graph: graph, Layout: placements, EdgeRoute: routes, Lease: lease}, nil
}

// YAML returns the canonical semantic serialization.
//
// The graph goes to the serializer; the layout is not loaded at all. Reading it
// and then not passing it on would be the filtering step D5 exists to avoid.
func (s *Service) YAML(ctx context.Context, diagramID uuid.UUID, p policy.Principal) ([]byte, error) {
	d, err := s.diagrams.ByID(ctx, s.db.Pool, diagramID)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireMember(ctx, s.db.Pool, p, d.ProjectID); err != nil {
		return nil, err
	}
	graph, err := s.diagrams.LoadGraph(ctx, s.db.Pool, diagramID)
	if err != nil {
		return nil, err
	}
	return serialize.Graph(graph)
}

// Revisions returns a diagram's revision history, newest first.
func (s *Service) Revisions(ctx context.Context, diagramID uuid.UUID, limit int, p policy.Principal) ([]*repo.Revision, error) {
	d, err := s.diagrams.ByID(ctx, s.db.Pool, diagramID)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireMember(ctx, s.db.Pool, p, d.ProjectID); err != nil {
		return nil, err
	}
	return s.diagrams.ListRevisions(ctx, s.db.Pool, diagramID, limit)
}

// ── leases ───────────────────────────────────────────────────────────────────

// LeaseHeldError reports that somebody else is editing.
//
// It names the holder and when their session lapses, because a refusal that
// says only "held" leaves the caller with nothing to do but retry blindly.
type LeaseHeldError struct {
	HolderName string
	HolderKind string
	ExpiresAt  time.Time
}

func (e *LeaseHeldError) Error() string {
	return fmt.Sprintf("%s is editing this diagram. Their session lapses at %s if they stop; try again then, or ask them to save.",
		e.HolderName, e.ExpiresAt.UTC().Format(time.RFC3339))
}

// LeaseLostError reports that the caller's own session has gone.
type LeaseLostError struct {
	ExpiredAt time.Time
	Since     []*repo.Revision
}

func (e *LeaseLostError) Error() string {
	if len(e.Since) == 0 {
		return fmt.Sprintf("Your edit session lapsed at %s. Nothing else has changed since, so take the lease again and save.",
			e.ExpiredAt.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("Your edit session lapsed at %s and %d save(s) landed while it was gone. Take the lease again and reconcile before saving.",
		e.ExpiredAt.UTC().Format(time.RFC3339), len(e.Since))
}

// AcquireLease takes the edit session.
func (s *Service) AcquireLease(ctx context.Context, diagramID uuid.UUID, p policy.Principal) (*repo.Lease, error) {
	var out *repo.Lease
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		d, err := s.diagrams.ByID(ctx, tx, diagramID)
		if err != nil {
			return err
		}
		subject, err := s.requireMember(ctx, tx, p, d.ProjectID)
		if err != nil {
			return err
		}
		// A lease is the right to make a semantic change, so it needs the same
		// authority as making one. Handing a developer a lease they can never
		// save under would be a worse experience than refusing here.
		if den := policy.CheckWrite(policy.KindDiagram, []string{"nodes"}, subject); den != nil {
			return den
		}

		actor := p.WritingActorID()
		ok, err := s.leases.Acquire(ctx, tx, diagramID, actor, d.CurrentRevision, s.ttl)
		if err != nil {
			return err
		}
		if !ok {
			held, err := s.leases.Get(ctx, tx, diagramID)
			if err != nil {
				return err
			}
			if held != nil {
				return &LeaseHeldError{HolderName: held.HolderName, HolderKind: held.HolderKind, ExpiresAt: held.ExpiresAt}
			}
			return fmt.Errorf("%w: the lease could not be taken", domain.ErrConflict)
		}

		out, err = s.leases.Get(ctx, tx, diagramID)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &d.ProjectID, ActorID: actor, OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionLeaseAcquired, SubjectID: &diagramID,
			After: map[string]any{"base_revision": d.CurrentRevision, "expires_at": out.ExpiresAt},
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Heartbeat extends a lease the caller still holds.
//
// Returns LeaseLostError when it has lapsed, so the client learns at heartbeat
// time — with the work still on screen — rather than at save time.
func (s *Service) Heartbeat(ctx context.Context, diagramID uuid.UUID, p policy.Principal) (*repo.Lease, error) {
	ok, err := s.leases.Heartbeat(ctx, s.db.Pool, diagramID, p.WritingActorID(), s.ttl)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, s.leaseLost(ctx, diagramID)
	}
	return s.leases.Get(ctx, s.db.Pool, diagramID)
}

// ReleaseLease drops the caller's lease.
func (s *Service) ReleaseLease(ctx context.Context, diagramID uuid.UUID, p policy.Principal) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		d, err := s.diagrams.ByID(ctx, tx, diagramID)
		if err != nil {
			return err
		}
		released, err := s.leases.Release(ctx, tx, diagramID, p.WritingActorID())
		if err != nil {
			return err
		}
		if !released {
			return fmt.Errorf("%w: you do not hold this diagram's lease", domain.ErrNotFound)
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &d.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionLeaseReleased, SubjectID: &diagramID,
		})
	})
}

// leaseLost builds the error for a lapsed session on the heartbeat path.
//
// Deliberately without a diff. The lease records the revision it was taken at,
// not the revision the client last saw, so counting saves since then counts the
// holder's own — "2 saves landed while your session was gone" when the holder
// made both of them, before it lapsed. The diff belongs on the save path, where
// the request carries a base revision that means something.
func (s *Service) leaseLost(ctx context.Context, diagramID uuid.UUID) error {
	lease, err := s.leases.Get(ctx, s.db.Pool, diagramID)
	if err != nil {
		return err
	}
	if lease != nil && lease.Live(time.Now()) {
		// Somebody else took it over after ours lapsed.
		return &LeaseHeldError{HolderName: lease.HolderName, HolderKind: lease.HolderKind, ExpiresAt: lease.ExpiresAt}
	}

	lost := &LeaseLostError{ExpiredAt: time.Now()}
	if lease != nil {
		lost.ExpiredAt = lease.ExpiresAt
	}
	return lost
}
