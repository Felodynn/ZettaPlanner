package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/layout"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	svc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
)

// ── wire shapes ──────────────────────────────────────────────────────────────

// DiagramSummary is a diagram without its contents.
type DiagramSummary struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug" doc:"Stable identity. Assigned at creation and never follows the name, so a rename is not a delete plus an add."`
	// Level is the diagram's altitude. Sequence diagrams are deferred to v0.1.
	Level      string `json:"level" enum:"context,container,component"`
	LayoutMode string `json:"layout_mode" enum:"auto,manual" doc:"Flips to manual on the first coordinate write and does not flip back without an explicit destructive confirm."`
	Revision   int    `json:"revision"`
}

// NodeView is one semantic node, plus its presentation state alongside — never
// mixed into it. The canvas needs both; nothing that talks to a model gets the
// second.
type NodeView struct {
	ID         uuid.UUID      `json:"id"`
	EntityID   uuid.UUID      `json:"entity_id"`
	Slug       string         `json:"slug"`
	Kind       string         `json:"kind" enum:"system,service,component,datastore,queue,external_system,actor,api,job,boundary,note"`
	Name       string         `json:"name"`
	Summary    string         `json:"summary,omitempty"`
	Attrs      map[string]any `json:"attrs"`
	Parent     string         `json:"parent,omitempty" doc:"Containment parent's slug. Only a boundary or a system may contain."`
	Provenance string         `json:"provenance" enum:"vlm_extracted,human_created,human_edited,agent_proposed,agent_committed,system"`
	Confidence *float32       `json:"confidence,omitempty" doc:"Set for vlm_extracted elements. Low confidence is a first-class value, never a guess."`

	Layout *PlacementView `json:"layout,omitempty" doc:"Presentation only. Never sent to a model."`
}

// PlacementView is a node's presentation state.
type PlacementView struct {
	X         *float64 `json:"x,omitempty"`
	Y         *float64 `json:"y,omitempty"`
	W         *float64 `json:"w,omitempty"`
	H         *float64 `json:"h,omitempty"`
	Collapsed bool     `json:"collapsed"`
	Unplaced  bool     `json:"unplaced" doc:"The node arrived without coordinates and was placed near its neighbours. The UI must make it visually distinct; the flag clears on the first human move."`
}

// EdgeView is one semantic edge.
type EdgeView struct {
	ID         uuid.UUID      `json:"id"`
	From       string         `json:"from" doc:"Source node's entity slug"`
	To         string         `json:"to"`
	Relation   string         `json:"relation" enum:"calls,depends_on,reads_from,writes_to,publishes_to,subscribes_to,contains,deploys_to,data_flow"`
	Label      string         `json:"label,omitempty"`
	Attrs      map[string]any `json:"attrs"`
	Provenance string         `json:"provenance"`
	Confidence *float32       `json:"confidence,omitempty"`
	Waypoints  []layout.Point `json:"waypoints,omitempty" doc:"Presentation only."`
}

// NoteView is one clarification.
type NoteView struct {
	ID          uuid.UUID  `json:"id"`
	Kind        string     `json:"kind" enum:"overlap,conflict,missing,clarification"`
	SubjectKind string     `json:"subject_kind"`
	SubjectID   *uuid.UUID `json:"subject_id,omitempty"`
	SubjectSlug string     `json:"subject_slug,omitempty"`
	Body        string     `json:"body"`
	Status      string     `json:"status" enum:"open,resolved,dismissed"`
	CreatedAt   time.Time  `json:"created_at"`
}

// LeaseView is a held edit session.
type LeaseView struct {
	HolderName       string    `json:"holder_name"`
	HolderKind       string    `json:"holder_kind" enum:"human,agent,system"`
	BaseRevision     int       `json:"base_revision"`
	ExpiresAt        time.Time `json:"expires_at"`
	HeartbeatSeconds int       `json:"heartbeat_seconds" doc:"How often the client should heartbeat. Published so the frontend does not hard-code a number that later disagrees with the server."`
	HeldByYou        bool      `json:"held_by_you"`
}

// DiagramView is a whole diagram: both layers, kept apart.
type DiagramView struct {
	DiagramSummary
	Nodes []NodeView `json:"nodes"`
	Edges []EdgeView `json:"edges"`
	Notes []NoteView `json:"notes"`
	Lease *LeaseView `json:"lease,omitempty"`
}

// RevisionView is one atomic save.
type RevisionView struct {
	Seq       int                  `json:"seq"`
	ActorID   uuid.UUID            `json:"actor_id"`
	Summary   string               `json:"summary,omitempty"`
	Changes   repo.RevisionChanges `json:"changes"`
	CreatedAt time.Time            `json:"created_at"`
}

// ── registration ─────────────────────────────────────────────────────────────

func (s *Server) registerDiagrams() {
	s.registerDiagramReads()
	s.registerLease()
	s.registerSave()
	s.registerLayout()
	s.registerEntitiesAndNotes()
}

func (s *Server) registerDiagramReads() {
	huma.Register(s.API, huma.Operation{
		OperationID: "list-diagrams",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/diagrams",
		Summary:     "A project's diagrams",
		Tags:        []string{"diagrams"},
	}, func(ctx context.Context, in *struct {
		PID uuid.UUID `path:"pid"`
	}) (*struct {
		Body struct {
			Diagrams []DiagramSummary `json:"diagrams"`
		}
	}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Diagrams.List(ctx, in.PID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &struct {
			Body struct {
				Diagrams []DiagramSummary `json:"diagrams"`
			}
		}{}
		out.Body.Diagrams = []DiagramSummary{}
		for _, d := range list {
			out.Body.Diagrams = append(out.Body.Diagrams, diagramSummary(d))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "create-diagram",
		Method:        http.MethodPost,
		Path:          "/api/v1/projects/{pid}/diagrams",
		Summary:       "Create a diagram",
		Description:   "Manager-owned. Created in `auto` layout mode: no coordinates are stored until somebody drags a node.",
		Tags:          []string{"diagrams"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		PID  uuid.UUID `path:"pid"`
		Body struct {
			Name  string `json:"name" minLength:"1"`
			Slug  string `json:"slug,omitempty" doc:"Optional. Derived from the name when omitted, and never changed afterwards."`
			Level string `json:"level" enum:"context,container,component"`
		}
	}) (*struct{ Body DiagramSummary }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		d, err := s.deps.Diagrams.Create(ctx, in.PID, svc.CreateInput{
			Name: in.Body.Name, Slug: in.Body.Slug, Level: dg.Level(in.Body.Level),
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &struct{ Body DiagramSummary }{Body: diagramSummary(d)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "get-diagram",
		Method:      http.MethodGet,
		Path:        "/api/v1/diagrams/{id}",
		Summary:     "A diagram's semantic state, presentation state and current revision",
		Description: "The two layers are returned side by side and never merged. `layout` on a node is presentation " +
			"and is not part of what any model is shown (D5).",
		Tags: []string{"diagrams"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{ Body DiagramView }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		view, err := s.deps.Diagrams.Get(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &struct{ Body DiagramView }{Body: s.diagramView(view, p.WritingActorID())}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "get-diagram-yaml",
		Method:      http.MethodGet,
		Path:        "/api/v1/diagrams/{id}/yaml",
		Summary:     "The canonical semantic serialization",
		Description: "Byte-deterministic, and structurally incapable of containing coordinates: the serializer's " +
			"only input type has no coordinate field, and it cannot import the layout store (D5, §6.2). " +
			"The same bytes feed the model context and the optional git projection.",
		Tags: []string{"diagrams"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*yamlOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		body, err := s.deps.Diagrams.YAML(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &yamlOutput{ContentType: "application/yaml; charset=utf-8", Body: body}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "list-diagram-revisions",
		Method:      http.MethodGet,
		Path:        "/api/v1/diagrams/{id}/revisions",
		Summary:     "A diagram's revision history",
		Description: "One revision per human save. Layout writes produce none.",
		Tags:        []string{"diagrams"},
	}, func(ctx context.Context, in *struct {
		ID    uuid.UUID `path:"id"`
		Limit int       `query:"limit" default:"50" minimum:"1" maximum:"200"`
	}) (*struct {
		Body struct {
			Revisions []RevisionView `json:"revisions"`
		}
	}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Diagrams.Revisions(ctx, in.ID, in.Limit, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &struct {
			Body struct {
				Revisions []RevisionView `json:"revisions"`
			}
		}{}
		out.Body.Revisions = revisionViews(list)
		return out, nil
	})
}

// yamlOutput carries a non-JSON body. huma writes []byte through unchanged.
type yamlOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

func (s *Server) registerLease() {
	huma.Register(s.API, huma.Operation{
		OperationID: "acquire-diagram-lease",
		Method:      http.MethodPost,
		Path:        "/api/v1/diagrams/{id}/lease",
		Summary:     "Take the edit session",
		Description: "Leases, not locks or CRDTs (D9). Returns 409 lease_held naming the current holder and when " +
			"their session lapses. A lapsed lease is taken over automatically, so a crashed editor cannot block " +
			"a diagram indefinitely.",
		Tags: []string{"diagrams"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{ Body LeaseView }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		lease, err := s.deps.Diagrams.AcquireLease(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &struct{ Body LeaseView }{Body: s.leaseView(lease, p.WritingActorID())}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "heartbeat-diagram-lease",
		Method:      http.MethodPost,
		Path:        "/api/v1/diagrams/{id}/lease/heartbeat",
		Summary:     "Extend the edit session",
		Description: "Fails once the session has lapsed, so the client finds out with the work still on screen " +
			"rather than at save time.",
		Tags: []string{"diagrams"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{ Body LeaseView }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		lease, err := s.deps.Diagrams.Heartbeat(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &struct{ Body LeaseView }{Body: s.leaseView(lease, p.WritingActorID())}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "release-diagram-lease",
		Method:        http.MethodDelete,
		Path:          "/api/v1/diagrams/{id}/lease",
		Summary:       "Give up the edit session",
		Tags:          []string{"diagrams"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.deps.Diagrams.ReleaseLease(ctx, in.ID, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return nil, nil
	})
}

func (s *Server) registerSave() {
	huma.Register(s.API, huma.Operation{
		OperationID: "save-diagram-revision",
		Method:      http.MethodPost,
		Path:        "/api/v1/diagrams/{id}/revisions",
		Summary:     "Save the whole diagram atomically",
		Description: "Send the complete semantic state and the revision you started from. If the base has moved, " +
			"the save is rejected with 409 stale_base_revision carrying the revisions that landed in between — " +
			"never merged. Nodes are identified by entity slug; unknown slugs create catalog entities, and a " +
			"plausible name collision records a Note rather than merging anything (§3.4).",
		Tags:          []string{"diagrams"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *saveInput) (*struct{ Body SaveResponse }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}

		save := svc.SaveInput{
			BaseRevision: in.Body.BaseRevision,
			Summary:      in.Body.Summary,
		}
		for _, n := range in.Body.Nodes {
			save.Nodes = append(save.Nodes, svc.NodeInput{
				Slug: n.Slug, Kind: dg.NodeKind(n.Kind), Name: n.Name, Summary: n.Summary,
				Attrs: n.Attrs, Parent: n.Parent, LabelOverride: n.LabelOverride,
				Confidence: n.Confidence, Provenance: dg.Provenance(n.Provenance),
			})
		}
		for _, e := range in.Body.Edges {
			save.Edges = append(save.Edges, svc.EdgeInput{
				From: e.From, To: e.To, Relation: dg.Relation(e.Relation), Label: e.Label,
				Attrs: e.Attrs, Confidence: e.Confidence, Provenance: dg.Provenance(e.Provenance),
			})
		}

		result, err := s.deps.Diagrams.Save(ctx, in.ID, save, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}

		body := SaveResponse{Revision: result.Revision, Changes: result.Changes, NotesRaised: []NoteView{}}
		for _, n := range result.NotesRaised {
			body.NotesRaised = append(body.NotesRaised, noteView(n))
		}
		return &struct{ Body SaveResponse }{Body: body}, nil
	})
}

type saveInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		BaseRevision int    `json:"base_revision" doc:"The revision this edit started from. A mismatch is rejected, never merged."`
		Summary      string `json:"summary,omitempty" doc:"What this save changed, in a line. Appears in the revision log and the git commit."`
		Nodes        []struct {
			Slug          string         `json:"slug" minLength:"1" doc:"Entity slug. This is the node's identity; an unknown one creates a catalog entity."`
			Kind          string         `json:"kind" enum:"system,service,component,datastore,queue,external_system,actor,api,job,boundary,note"`
			Name          string         `json:"name" minLength:"1"`
			Summary       string         `json:"summary,omitempty"`
			Attrs         map[string]any `json:"attrs,omitempty"`
			Parent        string         `json:"parent,omitempty" doc:"Containment parent's slug. Only a boundary or a system may contain."`
			LabelOverride string         `json:"label_override,omitempty" doc:"Renames the entity on this diagram only."`
			Confidence    *float32       `json:"confidence,omitempty" minimum:"0" maximum:"1"`
			Provenance    string         `json:"provenance,omitempty" enum:"vlm_extracted,human_created,human_edited,agent_proposed,agent_committed,system"`
		} `json:"nodes"`
		Edges []struct {
			From       string         `json:"from" minLength:"1"`
			To         string         `json:"to" minLength:"1"`
			Relation   string         `json:"relation" enum:"calls,depends_on,reads_from,writes_to,publishes_to,subscribes_to,contains,deploys_to,data_flow"`
			Label      string         `json:"label,omitempty"`
			Attrs      map[string]any `json:"attrs,omitempty"`
			Confidence *float32       `json:"confidence,omitempty" minimum:"0" maximum:"1"`
			Provenance string         `json:"provenance,omitempty"`
		} `json:"edges"`
	}
}

// SaveResponse is what a successful save produced.
type SaveResponse struct {
	Revision    int                  `json:"revision"`
	Changes     repo.RevisionChanges `json:"changes"`
	NotesRaised []NoteView           `json:"notes_raised" doc:"Clarifications this save produced — most often a possible duplicate entity, recorded rather than merged."`
}

func (s *Server) registerLayout() {
	huma.Register(s.API, huma.Operation{
		OperationID: "save-diagram-layout",
		Method:      http.MethodPut,
		Path:        "/api/v1/diagrams/{id}/layout",
		Summary:     "Write presentation state",
		Description: "Takes no lease and produces no revision: moving a box is not a semantic change. The first " +
			"write carrying a coordinate flips the diagram to `manual` permanently (§3.2). Any project member may " +
			"move a node — a coordinate is not an owned field.",
		Tags: []string{"diagrams"},
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body struct {
			Nodes []struct {
				NodeID    uuid.UUID `json:"node_id"`
				X         *float64  `json:"x,omitempty"`
				Y         *float64  `json:"y,omitempty"`
				W         *float64  `json:"w,omitempty"`
				H         *float64  `json:"h,omitempty"`
				Collapsed bool      `json:"collapsed,omitempty"`
			} `json:"nodes,omitempty"`
			Edges []struct {
				EdgeID    uuid.UUID      `json:"edge_id"`
				Waypoints []layout.Point `json:"waypoints"`
			} `json:"edges,omitempty"`
		}
	}) (*struct {
		Body struct {
			LayoutMode      string `json:"layout_mode" enum:"auto,manual"`
			FlippedToManual bool   `json:"flipped_to_manual" doc:"True when this write was the one that made the diagram manual. Automatic layout stops from here."`
		}
	}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}

		var input svc.LayoutInput
		for _, n := range in.Body.Nodes {
			input.Nodes = append(input.Nodes, svc.LayoutNodeInput{
				NodeID: n.NodeID, X: n.X, Y: n.Y, W: n.W, H: n.H, Collapsed: n.Collapsed,
			})
		}
		for _, e := range in.Body.Edges {
			input.Edges = append(input.Edges, svc.LayoutEdgeInput{EdgeID: e.EdgeID, Waypoints: e.Waypoints})
		}

		result, err := s.deps.Diagrams.SaveLayout(ctx, in.ID, input, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &struct {
			Body struct {
				LayoutMode      string `json:"layout_mode" enum:"auto,manual"`
				FlippedToManual bool   `json:"flipped_to_manual" doc:"True when this write was the one that made the diagram manual. Automatic layout stops from here."`
			}
		}{}
		out.Body.LayoutMode = result.Mode
		out.Body.FlippedToManual = result.FlippedToManual
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "reset-diagram-layout",
		Method:      http.MethodPost,
		Path:        "/api/v1/diagrams/{id}/layout/reset",
		Summary:     "Discard every stored coordinate and return to automatic layout",
		Description: "Destructive and irreversible: the hand-arranged positions are not recoverable from anything " +
			"else. Requires confirm=true, which is why this is not a field on the layout endpoint (§3.2).",
		Tags:          []string{"diagrams"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body struct {
			Confirm bool `json:"confirm" doc:"Must be true. Every hand-set position on this diagram is discarded."`
		}
	}) (*struct{}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.deps.Diagrams.ResetLayout(ctx, in.ID, in.Body.Confirm, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return nil, nil
	})
}

func (s *Server) registerEntitiesAndNotes() {
	huma.Register(s.API, huma.Operation{
		OperationID: "list-entities",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/entities",
		Summary:     "The project's architecture catalog",
		Description: "Entities are project-level identities. One entity may appear in several diagrams as the same thing.",
		Tags:        []string{"entities"},
	}, func(ctx context.Context, in *struct {
		PID uuid.UUID `path:"pid"`
	}) (*struct {
		Body struct {
			Entities []EntitySummary `json:"entities"`
		}
	}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Diagrams.Entities(ctx, in.PID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &struct {
			Body struct {
				Entities []EntitySummary `json:"entities"`
			}
		}{}
		out.Body.Entities = []EntitySummary{}
		for _, e := range list {
			out.Body.Entities = append(out.Body.Entities, entitySummary(e))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "get-entity",
		Method:      http.MethodGet,
		Path:        "/api/v1/entities/{id}",
		Summary:     "An entity, where it appears, and what it is connected to",
		Description: "`dependents` answers \"what depends on this\" across every diagram in the project, by " +
			"traversing typed edges. Structural relations (contains, deploys_to) are excluded: they say where " +
			"something sits, not what needs it.",
		Tags: []string{"entities"},
	}, func(ctx context.Context, in *struct {
		ID    uuid.UUID `path:"id"`
		Depth int       `query:"depth" default:"2" minimum:"1" maximum:"6" doc:"How many hops to traverse."`
	}) (*struct{ Body EntityDetailView }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		detail, err := s.deps.Diagrams.Entity(ctx, in.ID, in.Depth, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &struct{ Body EntityDetailView }{Body: entityDetailView(detail)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "list-notes",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/notes",
		Summary:     "Clarifications: overlaps, conflicts, missing pieces",
		Description: "Notes are how the backend and the agent say \"these two may be the same thing\" without " +
			"merging them. They live in Postgres and in assembled context, and are never chunked into the vector store.",
		Tags: []string{"notes"},
	}, func(ctx context.Context, in *struct {
		PID    uuid.UUID `path:"pid"`
		Status string    `query:"status" enum:"open,resolved,dismissed"`
	}) (*struct {
		Body struct {
			Notes []NoteView `json:"notes"`
		}
	}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Diagrams.Notes(ctx, in.PID, dg.NoteStatus(in.Status), p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &struct {
			Body struct {
				Notes []NoteView `json:"notes"`
			}
		}{}
		out.Body.Notes = []NoteView{}
		for _, n := range list {
			out.Body.Notes = append(out.Body.Notes, noteView(n))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "update-note",
		Method:      http.MethodPatch,
		Path:        "/api/v1/notes/{id}",
		Summary:     "Resolve or dismiss a note",
		Description: "Manager-owned: deciding that two things are or are not the same is an architectural judgement.",
		Tags:        []string{"notes"},
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body struct {
			Status string `json:"status" enum:"open,resolved,dismissed"`
		}
	}) (*struct{ Body NoteView }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		note, err := s.deps.Diagrams.SetNoteStatus(ctx, in.ID, dg.NoteStatus(in.Body.Status), p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &struct{ Body NoteView }{Body: noteView(note)}, nil
	})
}

// ── view builders ────────────────────────────────────────────────────────────

// EntitySummary is one catalog entity.
type EntitySummary struct {
	ID      uuid.UUID      `json:"id"`
	Kind    string         `json:"kind"`
	Name    string         `json:"name"`
	Slug    string         `json:"slug"`
	Summary string         `json:"summary,omitempty"`
	Attrs   map[string]any `json:"attrs"`
}

// PlacementSummary is one diagram an entity appears in.
type PlacementSummary struct {
	DiagramID   uuid.UUID `json:"diagram_id"`
	DiagramSlug string    `json:"diagram_slug"`
	DiagramName string    `json:"diagram_name"`
	Level       string    `json:"level"`
	NodeID      uuid.UUID `json:"node_id"`
}

// RelatedView is one entity reached by traversal.
type RelatedView struct {
	EntityID    uuid.UUID `json:"entity_id"`
	Slug        string    `json:"slug"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Summary     string    `json:"summary,omitempty"`
	Depth       int       `json:"depth" doc:"Hops from the starting entity. 1 is a direct relation."`
	Relation    string    `json:"relation" doc:"The relation on the first hop."`
	DiagramSlug string    `json:"diagram_slug" doc:"Which diagram that first hop was drawn in."`
	Label       string    `json:"label,omitempty"`
}

// EntityDetailView is an entity with its cross-diagram neighbourhood.
type EntityDetailView struct {
	EntitySummary
	Placements []PlacementSummary `json:"placements"`
	Dependents []RelatedView      `json:"dependents" doc:"What depends on this entity, across every diagram."`
	DependsOn  []RelatedView      `json:"depends_on"`
	Notes      []NoteView         `json:"notes"`
}

func diagramSummary(d *dg.Diagram) DiagramSummary {
	return DiagramSummary{
		ID: d.ID, Name: d.Name, Slug: d.Slug, Level: string(d.Level),
		LayoutMode: d.LayoutMode, Revision: d.CurrentRevision,
	}
}

func (s *Server) diagramView(v *svc.View, callerActor uuid.UUID) DiagramView {
	out := DiagramView{
		DiagramSummary: diagramSummary(v.Graph.Diagram),
		Nodes:          []NodeView{},
		Edges:          []EdgeView{},
		Notes:          []NoteView{},
	}

	slugByNodeID := map[uuid.UUID]string{}
	for _, n := range v.Graph.Nodes {
		slugByNodeID[n.ID] = n.Slug()
	}

	for _, n := range v.Graph.Nodes {
		view := NodeView{
			ID: n.ID, EntityID: n.EntityID, Slug: n.Slug(), Kind: string(n.Kind()),
			Name: n.Label(), Summary: n.Entity.Summary, Attrs: orEmptyAttrs(n.Entity.Attrs),
			Parent: n.ParentSlug, Provenance: string(n.Provenance), Confidence: n.Confidence,
		}
		if placement, ok := v.Layout[n.ID]; ok {
			view.Layout = &PlacementView{
				X: placement.X, Y: placement.Y, W: placement.W, H: placement.H,
				Collapsed: placement.Collapsed, Unplaced: placement.Unplaced,
			}
		}
		out.Nodes = append(out.Nodes, view)
	}

	for _, e := range v.Graph.Edges {
		out.Edges = append(out.Edges, EdgeView{
			ID: e.ID, From: slugByNodeID[e.FromNodeID], To: slugByNodeID[e.ToNodeID],
			Relation: string(e.Relation), Label: e.Label, Attrs: orEmptyAttrs(e.Attrs),
			Provenance: string(e.Provenance), Confidence: e.Confidence,
			Waypoints: v.EdgeRoute[e.ID],
		})
	}
	for _, n := range v.Graph.Notes {
		out.Notes = append(out.Notes, noteView(n))
	}
	if v.Lease != nil {
		lv := s.leaseView(v.Lease, callerActor)
		out.Lease = &lv
	}
	return out
}

func (s *Server) leaseView(l *repo.Lease, callerActor uuid.UUID) LeaseView {
	if l == nil {
		return LeaseView{}
	}
	return LeaseView{
		HolderName: l.HolderName, HolderKind: l.HolderKind,
		BaseRevision: l.BaseRevision, ExpiresAt: l.ExpiresAt,
		HeartbeatSeconds: int(svc.HeartbeatInterval.Seconds()),
		HeldByYou:        l.ActorID == callerActor,
	}
}

func noteView(n *dg.Note) NoteView {
	return NoteView{
		ID: n.ID, Kind: string(n.Kind), SubjectKind: n.SubjectKind, SubjectID: n.SubjectID,
		SubjectSlug: n.SubjectSlug, Body: n.Body, Status: string(n.Status), CreatedAt: n.CreatedAt,
	}
}

func entitySummary(e *dg.Entity) EntitySummary {
	return EntitySummary{
		ID: e.ID, Kind: string(e.Kind), Name: e.Name, Slug: e.Slug,
		Summary: e.Summary, Attrs: orEmptyAttrs(e.Attrs),
	}
}

func entityDetailView(d *svc.EntityDetail) EntityDetailView {
	out := EntityDetailView{
		EntitySummary: entitySummary(d.Entity),
		Placements:    []PlacementSummary{},
		Dependents:    []RelatedView{},
		DependsOn:     []RelatedView{},
		Notes:         []NoteView{},
	}
	for _, p := range d.Placements {
		out.Placements = append(out.Placements, PlacementSummary{
			DiagramID: p.DiagramID, DiagramSlug: p.DiagramSlug, DiagramName: p.DiagramName,
			Level: string(p.Level), NodeID: p.NodeID,
		})
	}
	for _, r := range d.Dependents {
		out.Dependents = append(out.Dependents, relatedView(r))
	}
	for _, r := range d.DependsOn {
		out.DependsOn = append(out.DependsOn, relatedView(r))
	}
	for _, n := range d.Notes {
		out.Notes = append(out.Notes, noteView(n))
	}
	return out
}

func relatedView(r repo.Related) RelatedView {
	return RelatedView{
		EntityID: r.EntityID, Slug: r.Slug, Kind: string(r.Kind), Name: r.Name,
		Summary: r.Summary, Depth: r.Depth, Relation: string(r.Relation),
		DiagramSlug: r.DiagramSlug, Label: r.Label,
	}
}

func revisionViews(list []*repo.Revision) []RevisionView {
	out := []RevisionView{}
	for _, r := range list {
		out = append(out, RevisionView{
			Seq: r.Seq, ActorID: r.ActorID, Summary: r.Summary,
			Changes: r.Changes, CreatedAt: r.CreatedAt,
		})
	}
	return out
}

func orEmptyAttrs(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
