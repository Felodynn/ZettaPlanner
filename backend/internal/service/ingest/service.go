// Package ingest orchestrates draw-to-product: upload, rasterize, extract per
// page, merge, review, accept.
//
// Extraction runs in the background because it is minutes of model time, and an
// HTTP request that holds a connection open for four pages of a PDF is a
// request that dies to the first proxy timeout and leaves no record of how far
// it got. The ingest row is the record; the client polls it, and from Phase 5
// the SSE stream pushes `ingest.progress` instead.
package ingest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Felodynn/ZettaPlanner/backend/internal/audit"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	ing "github.com/Felodynn/ZettaPlanner/backend/internal/domain/ingest"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/raster"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// MaxUploadBytes is the ceiling on one ingest's upload.
//
// 64 MiB: a dozen phone photographs or a scanned PDF of a notebook. Anything
// larger is not a diagram somebody drew, and refusing it here is cheaper than
// discovering it as a rasterizer that never returns.
const MaxUploadBytes = 64 << 20

// extractionBudget bounds one whole ingest's model work. Generous, because a
// cloud vision model on a 40-page PDF is genuinely slow, and finite, because a
// background job with no deadline is a goroutine leak with a nice name.
const extractionBudget = 45 * time.Minute

// interruptedAfter is how stale an in-flight ingest must look at boot before
// the recovery sweep declares it dead.
const interruptedAfter = 15 * time.Minute

// Service is the ingest application service.
type Service struct {
	db       *store.DB
	log      *slog.Logger
	registry *llm.Registry
	diagrams *diagramsvc.Service

	uploadsDir string

	ingests repo.Ingests
	members repo.Members

	// running tracks background extraction so a shutdown can wait for it rather
	// than leaving half-written pages and an ingest stuck in `extracting`.
	running sync.WaitGroup
}

// New builds the service.
func New(db *store.DB, log *slog.Logger, registry *llm.Registry, diagrams *diagramsvc.Service, uploadsDir string) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: db, log: log, registry: registry, diagrams: diagrams, uploadsDir: uploadsDir}
}

// Wait blocks until background extraction has finished. Called on shutdown.
func (s *Service) Wait() { s.running.Wait() }

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

// requireMember rejects a caller who cannot see the project at all.
func (s *Service) requireMember(ctx context.Context, q store.Querier, p policy.Principal, projectID uuid.UUID) (policy.Subject, error) {
	subject, err := s.subject(ctx, q, p, projectID)
	if err != nil {
		return subject, err
	}
	if !subject.IsMember && p.GlobalRole != policy.RoleAdmin {
		return subject, fmt.Errorf("%w: project", domain.ErrNotFound)
	}
	return subject, nil
}

// ── create ───────────────────────────────────────────────────────────────────

// Upload is one file as it arrived.
type Upload struct {
	Filename string
	Content  io.Reader
	Size     int64
}

// Create stores an upload, rasterizes it, and starts extraction.
//
// Membership is all it takes. Uploading a photograph writes no diagram content
// and binds nobody to anything — it is evidence gathering, like a worklog. The
// authority check that matters is on accept, which is the write.
func (s *Service) Create(ctx context.Context, projectID uuid.UUID, files []Upload, p policy.Principal) (*ing.Ingest, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: send at least one image or PDF as `files`", domain.ErrInvalid)
	}
	if _, err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}

	if _, err := s.vision(); err != nil {
		// Checked before a byte is written. An upload accepted into a system
		// with no vision model configured is an ingest that sits in `uploaded`
		// forever, and the operator finds out from a user.
		return nil, err
	}

	id := uuid.Must(uuid.NewV7())
	dir := filepath.Join(s.uploadsDir, projectID.String(), id.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating the upload directory: %w", err)
	}

	stored, err := s.store(dir, files)
	if err != nil {
		// Nothing has been recorded, so the half-written directory is the only
		// trace and it is ours to clean up.
		_ = os.RemoveAll(dir)
		return nil, err
	}

	pages, err := s.rasterize(ctx, dir, stored)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	kind := ing.SourceImage
	if stored[0].kind == raster.KindPDF {
		kind = ing.SourcePDF
	}

	now := time.Now()
	in := &ing.Ingest{
		ID: id, ProjectID: projectID, Status: ing.StatusUploaded,
		SourceKind: kind, SourceFilename: stored[0].name, SourcePath: stored[0].path,
		PageCount: len(pages), ActorID: p.WritingActorID(),
		CreatedAt: now, UpdatedAt: now,
	}
	if len(stored) > 1 {
		in.SourceFilename = fmt.Sprintf("%s and %d more", stored[0].name, len(stored)-1)
	}

	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.ingests.Create(ctx, tx, in); err != nil {
			return err
		}
		for _, pg := range pages {
			if err := s.ingests.InsertPage(ctx, tx, &ing.Page{
				ID: uuid.Must(uuid.NewV7()), IngestID: id, Idx: pg.Idx,
				ImagePath: pg.Path, Status: ing.PagePending,
			}); err != nil {
				return err
			}
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &projectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
			Action: audit.ActionIngestCreated, SubjectID: &id,
			After: map[string]any{
				"source_kind": string(kind), "pages": len(pages), "filename": in.SourceFilename,
			},
		})
	})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	s.start(ctx, in.ID)

	in.Pages = make([]*ing.Page, 0, len(pages))
	for _, pg := range pages {
		in.Pages = append(in.Pages, &ing.Page{IngestID: id, Idx: pg.Idx, ImagePath: pg.Path, Status: ing.PagePending})
	}
	return in, nil
}

type storedFile struct {
	name string
	path string
	kind raster.Kind
}

// store writes the uploaded bytes to disk, unmodified.
//
// The originals are kept forever. When an extraction turns out to be wrong the
// input is the evidence, and re-running against a re-derived image is neither
// free nor reproducible.
func (s *Service) store(dir string, files []Upload) ([]storedFile, error) {
	var (
		out   []storedFile
		total int64
		kinds = map[raster.Kind]bool{}
	)

	for i, f := range files {
		// The first bytes decide what this is. The client's Content-Type is not
		// consulted: browsers send application/octet-stream for anything they
		// do not recognise, and believing it means reporting "not an image"
		// about a file that is fine.
		head := make([]byte, 512)
		n, err := io.ReadFull(f.Content, head)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("reading %s: %w", f.Filename, err)
		}
		head = head[:n]

		kind, ext, err := raster.Sniff(head)
		if err != nil {
			return nil, fmt.Errorf("%w: %s — %s", domain.ErrInvalid, f.Filename, err.Error())
		}
		kinds[kind] = true

		path := filepath.Join(dir, fmt.Sprintf("source-%02d.%s", i, ext))
		dst, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		// Bounded by what actually arrives rather than by the declared size: a
		// Content-Length is a claim, and the ceiling has to hold against a
		// client that lies about it.
		body := io.MultiReader(bytes.NewReader(head), f.Content)
		written, err := io.Copy(dst, io.LimitReader(body, MaxUploadBytes-total+1))
		closeErr := dst.Close()
		if err != nil {
			return nil, fmt.Errorf("saving %s: %w", f.Filename, err)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		total += written
		if total > MaxUploadBytes {
			return nil, fmt.Errorf("%w: the upload is larger than %d MiB in total; send fewer pages or a smaller scan",
				domain.ErrInvalid, MaxUploadBytes>>20)
		}

		name := filepath.Base(f.Filename)
		if name == "" || name == "." || name == string(filepath.Separator) {
			name = fmt.Sprintf("upload-%d.%s", i+1, ext)
		}
		out = append(out, storedFile{name: name, path: path, kind: kind})
	}

	// One ingest is one document. A PDF and three photographs in the same
	// upload is two documents, and merging across them would invent
	// cross-references between drawings that were never on the same desk.
	if len(kinds) > 1 {
		return nil, fmt.Errorf("%w: this upload mixes a PDF with images. One ingest is one document — upload the PDF and the photographs separately",
			domain.ErrInvalid)
	}
	if kinds[raster.KindPDF] && len(out) > 1 {
		return nil, fmt.Errorf("%w: send one PDF per ingest. Its pages become the ingest's pages", domain.ErrInvalid)
	}
	return out, nil
}

// rasterize turns the stored files into page images.
func (s *Service) rasterize(ctx context.Context, dir string, files []storedFile) ([]raster.Page, error) {
	pagesDir := filepath.Join(dir, "pages")
	var out []raster.Page

	for i, f := range files {
		pages, err := raster.Convert(ctx, f.path, f.kind, raster.Options{
			Dir: pagesDir, StartIdx: len(out), SourceIdx: i,
		})
		if err != nil {
			if errors.Is(err, raster.ErrNoRasterizer) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %s could not be read as a page image — %s", domain.ErrInvalid, f.name, err.Error())
		}
		out = append(out, pages...)
		if len(out) > raster.MaxPages {
			return nil, fmt.Errorf("%w: this upload is %d pages and the limit is %d. A document that long is not a diagram somebody drew",
				domain.ErrInvalid, len(out), raster.MaxPages)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: the upload produced no pages", domain.ErrInvalid)
	}
	return out, nil
}

// vision resolves the model this ingest will use, once.
//
// Picked at the start and held for every page (D22). Falling back mid-ingest
// would mean pages 1 and 2 read by one model and pages 3 and 4 by another, and
// a merge across two models' confidence scales is a merge across two different
// meanings of 0.7.
func (s *Service) vision() (*llm.Client, error) {
	if s.registry == nil {
		return nil, fmt.Errorf("%w: no model registry is configured, so nothing can read a drawing", llm.ErrModelUnavailable)
	}
	candidates, err := s.registry.Candidates("vision")
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: no model is configured for the vision role; add one to models.yaml under roles.vision.prefer", llm.ErrModelUnavailable)
	}
	return candidates[0], nil
}

// ── reads ────────────────────────────────────────────────────────────────────

// Get returns an ingest with its per-page state and the merge.
func (s *Service) Get(ctx context.Context, id uuid.UUID, p policy.Principal) (*ing.Ingest, error) {
	in, err := s.ingests.WithPages(ctx, s.db.Pool, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireMember(ctx, s.db.Pool, p, in.ProjectID); err != nil {
		return nil, fmt.Errorf("%w: ingest", domain.ErrNotFound)
	}
	return in, nil
}

// List returns a project's ingests, newest first.
func (s *Service) List(ctx context.Context, projectID uuid.UUID, limit int, p policy.Principal) ([]*ing.Ingest, error) {
	if _, err := s.requireMember(ctx, s.db.Pool, p, projectID); err != nil {
		return nil, err
	}
	return s.ingests.ListByProject(ctx, s.db.Pool, projectID, limit)
}

// PageImage returns the path of one page image, for the review screen.
func (s *Service) PageImage(ctx context.Context, id uuid.UUID, idx int, p policy.Principal) (string, error) {
	in, err := s.Get(ctx, id, p)
	if err != nil {
		return "", err
	}
	page := in.PageByIdx(idx)
	if page == nil {
		return "", fmt.Errorf("%w: page %d of this ingest", domain.ErrNotFound, idx+1)
	}
	return page.ImagePath, nil
}

// ── background extraction ────────────────────────────────────────────────────

// start launches extraction for an ingest.
//
// The request's context is deliberately not used: it is cancelled the moment
// the response is written, which would kill every extraction the instant the
// upload returned 201. What is carried across is the request id, so the audit
// rows this work writes group with the upload that caused them — that is
// precisely what request_id is for.
func (s *Service) start(ctx context.Context, id uuid.UUID) {
	bg := telemetry.WithRequestID(context.WithoutCancel(ctx), telemetry.RequestID(ctx))

	s.running.Add(1)
	go func() {
		defer s.running.Done()
		runCtx, cancel := context.WithTimeout(bg, extractionBudget)
		defer cancel()
		if err := s.run(runCtx, id); err != nil {
			s.log.ErrorContext(runCtx, "ingest extraction failed", "ingest_id", id, "error", err)
		}
	}()
}

// run drives one ingest from uploaded to review.
func (s *Service) run(ctx context.Context, id uuid.UUID) error {
	if ok, err := s.ingests.Transition(ctx, s.db.Pool, id, ing.StatusUploaded, ing.StatusExtracting); err != nil {
		return err
	} else if !ok {
		// Something else already claimed it. Not an error: a retry racing the
		// first run is exactly the case the guard exists for.
		return nil
	}

	in, err := s.ingests.WithPages(ctx, s.db.Pool, id)
	if err != nil {
		return err
	}
	client, err := s.vision()
	if err != nil {
		return s.failIngest(ctx, in, "model_unavailable", err.Error())
	}

	s.extractPages(ctx, in, client, in.Pages)

	in, err = s.ingests.WithPages(ctx, s.db.Pool, id)
	if err != nil {
		return err
	}

	// Every page failed. There is nothing to review and nothing to merge, and
	// saying so is more useful than an empty review screen.
	if in.ExtractedPages() == 0 {
		detail := "No page could be read."
		if len(in.Pages) > 0 && in.Pages[0].ErrorDetail != "" {
			detail = fmt.Sprintf("No page could be read. The first failure was: %s", in.Pages[0].ErrorDetail)
		}
		return s.failIngest(ctx, in, "extraction_failed", detail)
	}

	return s.mergeAndReview(ctx, in, ing.StatusExtracting)
}

// extractPages runs the pages through the model, bounded by the model's own
// declared concurrency.
func (s *Service) extractPages(ctx context.Context, in *ing.Ingest, client *llm.Client, pages []*ing.Page) {
	limit := client.Config().Concurrency
	if limit < 1 {
		limit = 1
	}
	slots := make(chan struct{}, limit)

	var wg sync.WaitGroup
	for _, page := range pages {
		if page.Status == ing.PageExtracted {
			continue
		}
		wg.Add(1)
		go func(page *ing.Page) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			s.extractOne(ctx, in, client, page)
		}(page)
	}
	wg.Wait()
}

// extractOne reads one page and records the outcome, whatever it is.
func (s *Service) extractOne(ctx context.Context, in *ing.Ingest, client *llm.Client, page *ing.Page) {
	claimed, err := s.ingests.ClaimPage(ctx, s.db.Pool, in.ID, page.Idx)
	if err != nil {
		s.log.ErrorContext(ctx, "claiming ingest page", "ingest_id", in.ID, "page", page.Idx, "error", err)
		return
	}
	if !claimed {
		return // already in flight
	}

	started := time.Now()
	extraction, resp, err := s.extractPage(ctx, client, page.ImagePath, page.Idx, in.PageCount)
	elapsed := int(time.Since(started).Milliseconds())

	var raw string
	if resp != nil {
		raw = resp.Message.Content
	}

	if err != nil {
		class, detail := classify(err)
		// A failed page fails the page, not the ingest. The model's answer is
		// stored even though it was rejected — a malformed answer is the one
		// you most want to read.
		if ferr := s.ingests.FailPage(ctx, s.db.Pool, in.ID, page.Idx, class, truncateDetail(detail), raw); ferr != nil {
			s.log.ErrorContext(ctx, "recording ingest page failure", "ingest_id", in.ID, "page", page.Idx, "error", ferr)
		}
		s.log.WarnContext(ctx, "ingest page could not be read",
			"ingest_id", in.ID, "page", page.Idx, "class", class, "error", err)
		return
	}

	saved := &ing.Page{Extracted: extraction, Raw: raw, Model: client.Config().Model, DurationMS: elapsed}
	if resp != nil {
		saved.TokensIn, saved.TokensOut = resp.PromptEvalCount, resp.EvalCount
	}
	if err := s.ingests.SavePageExtraction(ctx, s.db.Pool, in.ID, page.Idx, saved); err != nil {
		s.log.ErrorContext(ctx, "saving ingest page extraction", "ingest_id", in.ID, "page", page.Idx, "error", err)
	}
}

// mergeAndReview runs the merge step and parks the ingest for a human.
func (s *Service) mergeAndReview(ctx context.Context, in *ing.Ingest, from ing.Status) error {
	if ok, err := s.ingests.Transition(ctx, s.db.Pool, in.ID, from, ing.StatusMerging); err != nil {
		return err
	} else if !ok {
		return nil
	}

	// The extracting → merging transition, recorded on its own. Merge is a
	// distinct step, and a state machine whose log skips a state cannot be
	// replayed by the v0.1 export.
	if err := audit.Write(ctx, s.db.Pool, audit.Entry{
		ProjectID: &in.ProjectID, ActorID: in.ActorID,
		Action: audit.ActionIngestExtracted, SubjectID: &in.ID,
		Before: map[string]any{"status": string(from)},
		After: map[string]any{
			"status":          string(ing.StatusMerging),
			"pages_extracted": in.ExtractedPages(), "pages_failed": in.FailedPages(),
		},
	}); err != nil {
		return err
	}

	merged := ing.Merge(in.Pages)

	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.ingests.SetMerged(ctx, tx, in.ID, merged); err != nil {
			return err
		}
		if ok, err := s.ingests.Transition(ctx, tx, in.ID, ing.StatusMerging, ing.StatusReview); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("%w: the ingest moved out of merging underneath us", domain.ErrConflict)
		}

		counts := merged.Counts()
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &in.ProjectID, ActorID: in.ActorID,
			Action: audit.ActionIngestMerged, SubjectID: &in.ID,
			// merging, not `from`. The extracting → merging hop is its own row
			// above; recording this one as extracting → review would make the
			// pair describe a state machine that skips a state, which is the
			// thing the extra row was added to prevent.
			Before: map[string]any{"status": string(ing.StatusMerging)},
			After: map[string]any{
				"status":     string(ing.StatusReview),
				"pages_read": merged.PagesRead, "pages_failed": merged.PagesFailed,
				"nodes": counts.Nodes, "edges": counts.Edges,
				"needs_review": counts.NeedsReview, "conflicts": counts.Conflicts,
			},
		})
	})
}

// failIngest records a terminal failure and its transition.
func (s *Service) failIngest(ctx context.Context, in *ing.Ingest, class, detail string) error {
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := s.ingests.Fail(ctx, tx, in.ID, class, truncateDetail(detail)); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{
			ProjectID: &in.ProjectID, ActorID: in.ActorID,
			Action: audit.ActionIngestFailed, SubjectID: &in.ID,
			Before: map[string]any{"status": string(in.Status)},
			After: map[string]any{
				"status": string(ing.StatusFailed), "error_class": class,
				"pages_failed": in.FailedPages(),
			},
		})
	})
}

// ── retry ────────────────────────────────────────────────────────────────────

// RetryPage re-reads one page and re-runs the merge.
//
// **Retry is per page**, because failure is per page. Re-running a five-page
// ingest to fix the one that was out of focus costs five model calls and
// discards four good extractions, and a human who has already reviewed the
// other four would have to review them again.
func (s *Service) RetryPage(ctx context.Context, id uuid.UUID, idx int, p policy.Principal) (*ing.Ingest, error) {
	in, err := s.ingests.WithPages(ctx, s.db.Pool, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.requireMember(ctx, s.db.Pool, p, in.ProjectID); err != nil {
		return nil, fmt.Errorf("%w: ingest", domain.ErrNotFound)
	}
	if in.Status == ing.StatusAccepted {
		return nil, &ing.TransitionError{From: in.Status, To: ing.StatusExtracting}
	}
	page := in.PageByIdx(idx)
	if page == nil {
		return nil, fmt.Errorf("%w: page %d of this ingest", domain.ErrNotFound, idx+1)
	}

	client, err := s.vision()
	if err != nil {
		return nil, err
	}

	from := in.Status
	if ok, err := s.ingests.Transition(ctx, s.db.Pool, id, from, ing.StatusExtracting); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: this ingest is busy; try again in a moment", domain.ErrConflict)
	}

	if err := audit.Write(ctx, s.db.Pool, audit.Entry{
		ProjectID: &in.ProjectID, ActorID: p.WritingActorID(), OnBehalfOf: p.OnBehalfOf(),
		Action: audit.ActionIngestPageRetried, SubjectID: &in.ID,
		Before: map[string]any{"status": string(from), "attempts": page.Attempts, "error_class": page.ErrorClass},
		After:  map[string]any{"status": string(ing.StatusExtracting), "page_idx": idx},
	}); err != nil {
		return nil, err
	}

	bg := telemetry.WithRequestID(context.WithoutCancel(ctx), telemetry.RequestID(ctx))
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		runCtx, cancel := context.WithTimeout(bg, extractionBudget)
		defer cancel()

		s.extractOne(runCtx, in, client, page)

		fresh, err := s.ingests.WithPages(runCtx, s.db.Pool, id)
		if err != nil {
			s.log.ErrorContext(runCtx, "reloading ingest after retry", "ingest_id", id, "error", err)
			return
		}
		if fresh.ExtractedPages() == 0 {
			detail := "No page could be read, including after a retry."
			if err := s.failIngest(runCtx, fresh, "extraction_failed", detail); err != nil {
				s.log.ErrorContext(runCtx, "failing ingest after retry", "ingest_id", id, "error", err)
			}
			return
		}
		if err := s.mergeAndReview(runCtx, fresh, ing.StatusExtracting); err != nil {
			s.log.ErrorContext(runCtx, "re-merging after retry", "ingest_id", id, "error", err)
		}
	}()

	in.Status = ing.StatusExtracting
	page.Status = ing.PageExtracting
	return in, nil
}

// ── recovery ─────────────────────────────────────────────────────────────────

// RecoverInterrupted marks ingests that a killed process left mid-flight.
//
// Called at boot. Without it an ingest stuck in `extracting` is indistinguishable
// from one still working, and the difference matters to the person waiting for
// it. Failed rather than restarted: the pages already read are kept, and a human
// retries the ones that were not — restarting automatically would spend model
// budget on work nobody is waiting for any more.
func (s *Service) RecoverInterrupted(ctx context.Context) (int, error) {
	stale, err := s.ingests.Unfinished(ctx, s.db.Pool, interruptedAfter)
	if err != nil {
		return 0, err
	}
	recovered := 0
	for _, in := range stale {
		full, err := s.ingests.WithPages(ctx, s.db.Pool, in.ID)
		if err != nil {
			return recovered, err
		}
		if err := s.failIngest(ctx, full, "interrupted",
			"The server restarted while this ingest was being read. Pages already extracted are kept; retry the rest."); err != nil {
			return recovered, err
		}
		recovered++
	}
	if recovered > 0 {
		s.log.InfoContext(ctx, "marked interrupted ingests as failed", "count", recovered)
	}
	return recovered, nil
}
