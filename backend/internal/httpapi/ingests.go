package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	dg "github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	ing "github.com/Felodynn/ZettaPlanner/backend/internal/domain/ingest"
	svc "github.com/Felodynn/ZettaPlanner/backend/internal/service/ingest"
)

// ── wire shapes ──────────────────────────────────────────────────────────────

// IngestSummary is one upload without its extraction.
type IngestSummary struct {
	ID     uuid.UUID `json:"id"`
	Status string    `json:"status" enum:"uploaded,extracting,merging,review,accepted,failed"`
	// SourceKind is what was uploaded. A PDF becomes one page per PDF page;
	// images become one page each.
	SourceKind     string     `json:"source_kind" enum:"image,pdf"`
	SourceFilename string     `json:"source_filename,omitempty"`
	PageCount      int        `json:"page_count"`
	DiagramID      *uuid.UUID `json:"diagram_id,omitempty" doc:"Set once the ingest is accepted."`
	ErrorClass     string     `json:"error_class,omitempty"`
	ErrorDetail    string     `json:"error_detail,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// IngestPageView is one page's extraction state.
type IngestPageView struct {
	Idx    int    `json:"idx" doc:"0-based and dense. A failed page keeps its slot."`
	Status string `json:"status" enum:"pending,extracting,extracted,failed"`

	// Extracted is this page's model output, parsed and validated. Shown so a
	// reviewer can see what the model said about one page before the merge
	// reconciled it with the others.
	Extracted *ing.PageExtraction `json:"extracted,omitempty"`

	// Raw is the model's answer byte for byte, kept forever and returned on
	// success and failure alike. `extracted` has already been through JSONB and
	// a Go struct; this is the only field that can answer "what did the model
	// actually say".
	Raw string `json:"raw,omitempty"`

	Model       string     `json:"model,omitempty"`
	TokensIn    int        `json:"tokens_in,omitempty"`
	TokensOut   int        `json:"tokens_out,omitempty"`
	DurationMS  int        `json:"duration_ms,omitempty"`
	Attempts    int        `json:"attempts"`
	ErrorClass  string     `json:"error_class,omitempty"`
	ErrorDetail string     `json:"error_detail,omitempty"`
	ExtractedAt *time.Time `json:"extracted_at,omitempty"`
}

// IngestView is an ingest and everything a review screen needs.
type IngestView struct {
	IngestSummary
	Pages []IngestPageView `json:"pages"`

	// Merged is the merge step's output: the cross-page reconciliation, the
	// disagreements it refused to settle, and the arrows it could not place.
	// Null until the ingest reaches review.
	Merged *ing.Merged `json:"merged,omitempty"`

	// Counts summarises what a human is being asked to look at.
	Counts *ing.ReviewCounts `json:"counts,omitempty"`
}

// AcceptResponse is what an accepted ingest produced.
type AcceptResponse struct {
	DiagramID   uuid.UUID  `json:"diagram_id"`
	DiagramSlug string     `json:"diagram_slug"`
	Revision    int        `json:"revision"`
	NodesAdded  int        `json:"nodes_added"`
	EdgesAdded  int        `json:"edges_added"`
	Dropped     []string   `json:"dropped" doc:"Elements you discarded during review."`
	Notes       []NoteView `json:"notes" doc:"Clarifications this accept recorded: unplaceable arrows, page disagreements, elements accepted below the confidence threshold."`
}

// ── registration ─────────────────────────────────────────────────────────────

func (s *Server) registerIngests() {
	s.registerIngestUpload()
	s.registerIngestReads()
	s.registerIngestActions()
}

// ingestUploadInput is the multipart upload.
//
// The declared content types are documentation: they tell a generated client
// what to send, and they appear in the OpenAPI document. They are deliberately
// not the check. `application/octet-stream` is in the list because including it
// makes huma's mime validator permissive, and permissive is correct here — a
// part's declared type is a claim by the client, browsers send
// application/octet-stream for anything they do not recognise, and rejecting on
// it means refusing a perfectly good JPEG because it had no file extension.
// What the file actually is gets decided by its magic number, in raster.Sniff,
// whose refusal names the formats a user can convert to.
type ingestUploadInput struct {
	PID     uuid.UUID `path:"pid"`
	RawBody huma.MultipartFormFiles[struct {
		Files []huma.FormFile `form:"files" contentType:"image/jpeg,image/png,image/gif,image/webp,image/bmp,application/pdf,application/octet-stream" required:"true"`
	}]
}

func (s *Server) registerIngestUpload() {
	huma.Register(s.API, huma.Operation{
		OperationID: "create-ingest",
		Method:      http.MethodPost,
		Path:        "/api/v1/projects/{pid}/ingests",
		Summary:     "Upload a drawing to be read into the architecture graph",
		Description: "Send photographs as several `files` parts, or one PDF. One ingest is one document: a PDF and " +
			"loose photographs in the same upload would be merged across drawings that were never on the same desk, " +
			"so it is refused.\n\n" +
			"Extraction runs in the background — one model call per page, never one for the document — and the " +
			"response returns immediately with the ingest in `uploaded`. Poll `GET /api/v1/ingests/{id}` for " +
			"progress. Nothing here writes to a diagram; accept does.\n\n" +
			"Any project member may upload: a photograph binds nobody to anything. Accepting it is the write, and " +
			"that needs diagram authority.",
		Tags:          []string{"ingest"},
		DefaultStatus: http.StatusCreated,
		MaxBodyBytes:  svc.MaxUploadBytes,
	}, func(ctx context.Context, in *ingestUploadInput) (*struct{ Body IngestView }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}

		files := in.RawBody.Data().Files
		uploads := make([]svc.Upload, 0, len(files))
		for _, f := range files {
			if !f.IsSet {
				continue
			}
			defer f.Close()
			uploads = append(uploads, svc.Upload{Filename: f.Filename, Content: f, Size: f.Size})
		}

		created, err := s.deps.Ingests.Create(ctx, in.PID, uploads, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &struct{ Body IngestView }{Body: ingestView(created)}, nil
	})
}

func (s *Server) registerIngestReads() {
	huma.Register(s.API, huma.Operation{
		OperationID: "list-ingests",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/ingests",
		Summary:     "A project's ingests, newest first",
		Tags:        []string{"ingest"},
	}, func(ctx context.Context, in *struct {
		PID   uuid.UUID `path:"pid"`
		Limit int       `query:"limit" default:"50" minimum:"1" maximum:"200"`
	}) (*struct {
		Body struct {
			Ingests []IngestSummary `json:"ingests"`
		}
	}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		list, err := s.deps.Ingests.List(ctx, in.PID, in.Limit, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &struct {
			Body struct {
				Ingests []IngestSummary `json:"ingests"`
			}
		}{}
		out.Body.Ingests = []IngestSummary{}
		for _, i := range list {
			out.Body.Ingests = append(out.Body.Ingests, ingestSummary(i))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "get-ingest",
		Method:      http.MethodGet,
		Path:        "/api/v1/ingests/{id}",
		Summary:     "An ingest's status, per-page extraction and merged payload",
		Description: "`pages[].extracted` is exactly what the model returned for that page, kept forever. `merged` " +
			"is the separate reconciliation step: what the pages agreed on, what they disagreed about, and which " +
			"arrows ran off a page and found nothing.\n\n" +
			"Every element carries a confidence, and `needs_review` says why one is flagged. An element the model " +
			"could not read has `legible: false` and cannot be accepted without a name.",
		Tags: []string{"ingest"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{ Body IngestView }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		got, err := s.deps.Ingests.Get(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &struct{ Body IngestView }{Body: ingestView(got)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "get-ingest-page-image",
		Method:      http.MethodGet,
		Path:        "/api/v1/ingests/{id}/pages/{idx}/image",
		Summary:     "The page image the model was shown",
		Description: "The normalized page: rotation applied, downscaled. Reviewing an extraction against the " +
			"original photograph rather than against this one means comparing to something the model never saw.",
		Tags: []string{"ingest"},
	}, func(ctx context.Context, in *struct {
		ID  uuid.UUID `path:"id"`
		Idx int       `path:"idx" minimum:"0"`
	}) (*imageOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		path, err := s.deps.Ingests.PageImage(ctx, in.ID, in.Idx, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &imageOutput{
			ContentType: contentTypeFor(path),
			// Private: a page image is project data, and an intermediary that
			// cached it would serve one project's whiteboard to another.
			CacheControl: "private, max-age=3600",
			Body:         body,
		}, nil
	})
}

// imageOutput carries a page image. huma writes []byte through unchanged.
type imageOutput struct {
	ContentType  string `header:"Content-Type"`
	CacheControl string `header:"Cache-Control"`
	Body         []byte
}

func contentTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	}
	return "application/octet-stream"
}

func (s *Server) registerIngestActions() {
	huma.Register(s.API, huma.Operation{
		OperationID: "retry-ingest-page",
		Method:      http.MethodPost,
		Path:        "/api/v1/ingests/{id}/pages/{idx}/retry",
		Summary:     "Re-read one page",
		Description: "Retry is per page because failure is per page. Re-running the whole ingest to fix the one " +
			"page that was out of focus costs a model call for every page and discards extractions somebody has " +
			"already reviewed. The merge re-runs afterwards.",
		Tags:          []string{"ingest"},
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *struct {
		ID  uuid.UUID `path:"id"`
		Idx int       `path:"idx" minimum:"0"`
	}) (*struct{ Body IngestView }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		retried, err := s.deps.Ingests.RetryPage(ctx, in.ID, in.Idx, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &struct{ Body IngestView }{Body: ingestView(retried)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "accept-ingest",
		Method:      http.MethodPost,
		Path:        "/api/v1/ingests/{id}/accept",
		Summary:     "Apply a reviewed ingest to a diagram",
		Description: "Creates a diagram, or adds to one. **Additive**: nothing is removed. A drawing is evidence " +
			"that these things exist, never evidence that everything else does not, and a whole-state save from a " +
			"photograph would delete every node the drawing happened not to include.\n\n" +
			"An element the model could not read cannot reach the canvas under an invented name. Every one of them " +
			"needs a `resolutions` entry giving a name or setting `drop`; the refusal names each element and quotes " +
			"whatever the model could make out.\n\n" +
			"Arrows that ran off a page and found nothing are recorded as Notes rather than drawn or discarded. " +
			"Accepted elements carry provenance `vlm_extracted` and their confidence; anything you renamed carries " +
			"`human_edited` and no confidence, because you read it.",
		Tags:          []string{"ingest"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *acceptIngestInput) (*struct{ Body AcceptResponse }, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}

		accept := svc.AcceptInput{
			Name: in.Body.Name, Slug: in.Body.Slug,
			Level: dg.Level(in.Body.Level), Summary: in.Body.Summary,
		}
		if in.Body.DiagramID != nil {
			accept.DiagramID = *in.Body.DiagramID
		}
		for _, r := range in.Body.Resolutions {
			accept.Resolutions = append(accept.Resolutions, svc.Resolution{
				Slug: r.Slug, Name: r.Name, Kind: dg.NodeKind(r.Kind), Drop: r.Drop,
			})
		}

		result, err := s.deps.Ingests.Accept(ctx, in.ID, accept, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}

		body := AcceptResponse{
			DiagramID: result.DiagramID, DiagramSlug: result.DiagramSlug,
			Revision: result.Revision, NodesAdded: result.NodesAdded, EdgesAdded: result.EdgesAdded,
			Dropped: result.Dropped, Notes: []NoteView{},
		}
		if body.Dropped == nil {
			body.Dropped = []string{}
		}
		for _, n := range result.Notes {
			body.Notes = append(body.Notes, noteView(n))
		}
		return &struct{ Body AcceptResponse }{Body: body}, nil
	})
}

type acceptIngestInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		DiagramID *uuid.UUID `json:"diagram_id,omitempty" doc:"Add to this diagram. Omit to create one."`
		Name      string     `json:"name,omitempty" doc:"The new diagram's name. Defaults to the title read off the page."`
		Slug      string     `json:"slug,omitempty" doc:"Stable identity. Derived from the name when omitted."`
		Level     string     `json:"level,omitempty" enum:"context,container,component" doc:"Required when creating a diagram. A sketch almost never says which altitude it is drawn at, and guessing it wrong mislabels the diagram permanently."`
		Summary   string     `json:"summary,omitempty" doc:"Revision summary. Appears in the diagram's history."`

		Resolutions []struct {
			Slug string `json:"slug" minLength:"1" doc:"The merged element's slug. Unreadable boxes get a positional placeholder such as unreadable-p2-1."`
			Name string `json:"name,omitempty" doc:"What it actually says. Supplying one marks the element human_edited and drops the model's confidence, because you read it."`
			Kind string `json:"kind,omitempty" enum:"system,service,component,datastore,queue,external_system,actor,api,job,boundary,note"`
			Drop bool   `json:"drop,omitempty" doc:"Discard this element and every arrow touching it."`
		} `json:"resolutions,omitempty"`
	}
}

// ── view builders ────────────────────────────────────────────────────────────

func ingestSummary(i *ing.Ingest) IngestSummary {
	return IngestSummary{
		ID: i.ID, Status: string(i.Status), SourceKind: string(i.SourceKind),
		SourceFilename: i.SourceFilename, PageCount: i.PageCount, DiagramID: i.DiagramID,
		ErrorClass: i.ErrorClass, ErrorDetail: i.ErrorDetail,
		CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
	}
}

func ingestView(i *ing.Ingest) IngestView {
	out := IngestView{IngestSummary: ingestSummary(i), Pages: []IngestPageView{}}
	for _, p := range i.Pages {
		out.Pages = append(out.Pages, IngestPageView{
			Idx: p.Idx, Status: string(p.Status), Extracted: p.Extracted, Raw: p.Raw,
			Model: p.Model, TokensIn: p.TokensIn, TokensOut: p.TokensOut,
			DurationMS: p.DurationMS, Attempts: p.Attempts,
			ErrorClass: p.ErrorClass, ErrorDetail: p.ErrorDetail, ExtractedAt: p.ExtractedAt,
		})
	}
	if i.Merged != nil {
		counts := i.Merged.Counts()
		out.Merged = i.Merged
		out.Counts = &counts
	}
	return out
}
