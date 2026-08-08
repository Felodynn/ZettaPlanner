package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Felodynn/ZettaPlanner/backend/internal/agent"
	"github.com/Felodynn/ZettaPlanner/backend/internal/config"
	"github.com/Felodynn/ZettaPlanner/backend/internal/httpapi"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/auth"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/contextsvc"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/governance"
	ingestsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/ingest"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/project"
	threadsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// boot loads configuration and installs the logger. Every command starts here.
func boot() (*config.Config, *slog.Logger, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	log, err := telemetry.Setup(telemetry.Options{Level: cfg.LogLevel, Format: cfg.LogFormat})
	if err != nil {
		return nil, nil, err
	}
	return cfg, log, nil
}

// stateDir is where the probe cache lives, alongside models.yaml.
func stateDir(cfg *config.Config) string { return filepath.Dir(cfg.ModelsPath) }

// ── serve ────────────────────────────────────────────────────────────────────

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "", "listen address (overrides ZP_ADDR)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, err := boot()
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.Addr = *addr
	}

	requestCtx := telemetry.WithRequestID(ctx, telemetry.NewRequestID())

	db, err := store.Open(requestCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	version, err := store.SchemaVersion(requestCtx, db.Pool)
	if err != nil || version == 0 {
		return fmt.Errorf("the database has no schema; run `zettaplanner migrate up` first")
	}

	registry, err := validateModels(requestCtx, cfg, db, log, llm.ValidateOptions{
		CacheDir: stateDir(cfg),
		Log:      log,
	})
	if err != nil {
		return err
	}

	if err := os.MkdirAll(cfg.UploadsDir, 0o750); err != nil {
		return fmt.Errorf("creating the uploads directory %s: %w", cfg.UploadsDir, err)
	}

	authSvc := auth.New(db, log, cfg.SessionTTL)
	projectSvc := project.New(db, log)
	diagramSvc := diagramsvc.New(db, log, cfg.LeaseTTL)
	ingestSvc := ingestsvc.New(db, log, registry, diagramSvc, cfg.UploadsDir)
	contextSvc := contextsvc.New(db, log, registry)
	planSvc := plansvc.New(db, log, contextSvc)

	// The hub is built here rather than by the server: the agent runner and the
	// thread service publish into it and both exist before the server does.
	events := httpapi.NewEventHub()
	runner := agent.New(agent.Deps{
		DB: db, Log: log, Registry: registry,
		Context: contextSvc, Plan: planSvc, Diagrams: diagramSvc, Events: events,
	})
	threadSvc := threadsvc.New(db, log, runner, events)
	governanceSvc := governance.New(db, log, planSvc, diagramSvc, events, registry)

	// A turn left running by a killed process blocks its project's queue until
	// the lease would lapse, and after a restart nothing is going to heartbeat
	// it. Swept once at boot, before anything can look.
	if n, err := runner.RecoverInterrupted(requestCtx); err != nil {
		log.WarnContext(requestCtx, "could not sweep interrupted turns", "error", err)
	} else if n > 0 {
		log.WarnContext(requestCtx, "turns abandoned at boot; their processes did not survive", "count", n)
	}
	runner.Start(requestCtx)

	// An ingest left mid-flight by a killed process is indistinguishable from
	// one still working, and the difference matters to whoever is waiting for
	// it. Swept once at boot, before anything can look.
	if _, err := ingestSvc.RecoverInterrupted(requestCtx); err != nil {
		log.WarnContext(requestCtx, "could not sweep interrupted ingests", "error", err)
	}

	srv := httpapi.New(httpapi.Deps{
		DB:            db,
		Registry:      registry,
		Auth:          authSvc,
		Projects:      projectSvc,
		Diagrams:      diagramSvc,
		Ingests:       ingestSvc,
		Plan:          planSvc,
		Context:       contextSvc,
		Threads:       threadSvc,
		Governance:    governanceSvc,
		Events:        events,
		Log:           log,
		Started:       time.Now(),
		SecureCookies: cfg.SecureCookies,
		TrustProxy:    cfg.TrustProxy,
	})
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler,
		ReadHeaderTimeout: 15 * time.Second,
		// No WriteTimeout: SSE streams for the length of an agent turn
		// (CONTRACT §7.2), and a write deadline would cut them off.
		IdleTimeout: 120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.InfoContext(ctx, "listening", "addr", cfg.Addr, "schema_version", version)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		// Close the event streams first: they are long-lived by design, and
		// http.Server.Shutdown waits for every in-flight request, which for an
		// SSE connection means forever.
		srv.Close()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		// A turn in flight is finished rather than killed. One cut mid-write
		// leaves a proposal half raised, and the whole point of the lease is
		// that somebody can tell afterwards what happened.
		runner.Stop()
		// Background extraction outlives the request that started it, so the
		// process has to outlive the extraction. Cut short, it leaves pages
		// claimed and an ingest stuck in `extracting` for the recovery sweep to
		// find on the next boot.
		ingestSvc.Wait()
		return nil
	}
}

// ── migrate ──────────────────────────────────────────────────────────────────

func cmdMigrate(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("migrate needs a subcommand: up, down, or status")
	}
	cfg, log, err := boot()
	if err != nil {
		return err
	}
	ctx = telemetry.WithRequestID(ctx, telemetry.NewRequestID())

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	switch args[0] {
	case "up":
		applied, err := store.MigrateUp(ctx, db, log)
		if err != nil {
			return err
		}
		if len(applied) == 0 {
			version, _ := store.SchemaVersion(ctx, db.Pool)
			fmt.Printf("nothing to do; schema is at version %d\n", version)
			return nil
		}
		for _, m := range applied {
			fmt.Printf("applied %04d_%s\n", m.Version, m.Name)
		}
		return nil

	case "down":
		rolled, err := store.MigrateDown(ctx, db, log)
		if err != nil {
			return err
		}
		if rolled == nil {
			fmt.Println("nothing to revert")
			return nil
		}
		fmt.Printf("reverted %04d_%s\n", rolled.Version, rolled.Name)
		return nil

	case "status":
		version, err := store.SchemaVersion(ctx, db.Pool)
		if err != nil {
			return err
		}
		dim, _ := db.EmbeddingDim(ctx)
		fmt.Printf("schema version:  %d\n", version)
		fmt.Printf("chunks.embedding: VECTOR(%d)\n", dim)
		return nil

	default:
		return fmt.Errorf("unknown migrate subcommand %q; use up, down, or status", args[0])
	}
}

// ── models ───────────────────────────────────────────────────────────────────

func cmdModels(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "check" {
		return errors.New("models needs a subcommand: check")
	}
	fs := flag.NewFlagSet("models check", flag.ContinueOnError)
	full := fs.Bool("full-probe", false,
		"prove a cloud model's entire configured window instead of the default floor; costs num_ctx input tokens per model")
	noCache := fs.Bool("no-cache", false, "ignore any cached probe result")
	asJSON := fs.Bool("json", false, "emit the reports as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	cfg, log, err := boot()
	if err != nil {
		return err
	}
	ctx = telemetry.WithRequestID(ctx, telemetry.NewRequestID())

	// The chunks.embedding dimension check needs the database, but a model
	// registry should still be checkable without one — a fresh machine has
	// models before it has a schema.
	var db *store.DB
	if opened, err := store.Open(ctx, cfg.DatabaseURL); err == nil {
		db = opened
		defer db.Close()
	} else {
		log.WarnContext(ctx, "database unavailable; the embedding dimension will not be checked against chunks.embedding",
			"error", err)
	}

	registry, verr := validateModels(ctx, cfg, db, log, llm.ValidateOptions{
		FullProbe: *full,
		NoCache:   *noCache,
		CacheDir:  stateDir(cfg),
		Log:       log,
	})
	if registry == nil {
		// The registry failed to load at all — a parse error or a structural
		// problem in models.yaml. There is nothing to report per model; the
		// error already names the file and the fix.
		return verr
	}

	reports := registry.Reports()
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reports); err != nil {
			return err
		}
		return verr
	}
	printReports(reports)
	return verr
}

// validateModels loads the registry and runs the tiered startup checks.
func validateModels(ctx context.Context, cfg *config.Config, db *store.DB, log *slog.Logger, opts llm.ValidateOptions) (*llm.Registry, error) {
	models, err := config.LoadModels(cfg.ModelsPath)
	if err != nil {
		return nil, err
	}
	registry := llm.NewRegistry(models)

	if cfg.SkipModelValidation {
		log.WarnContext(ctx, "ZP_SKIP_MODEL_VALIDATION is set; no model was checked. "+
			"A model whose num_ctx was silently ignored will present as amnesia, not as an error.")
		return registry, nil
	}

	if db != nil && opts.ChunkDim == 0 {
		dim, err := db.EmbeddingDim(ctx)
		if err != nil {
			return nil, err
		}
		opts.ChunkDim = dim
	}
	if _, err := registry.Validate(ctx, opts); err != nil {
		printReports(registry.Reports())
		return registry, err
	}
	return registry, nil
}

func printReports(reports []*llm.Report) {
	for _, r := range reports {
		mark := "ok  "
		if r.Failed() {
			mark = "FAIL"
		}
		fmt.Printf("\n[%s] %s — %s at %s (%s)\n", mark, r.Name, r.Model, r.Endpoint, r.Tier)

		if r.ConfiguredCtx > 0 {
			line := fmt.Sprintf("       num_ctx %d", r.ConfiguredCtx)
			if r.MaxContext > 0 {
				line += fmt.Sprintf(" of a maximum %d", r.MaxContext)
			}
			switch r.ProofMethod {
			case llm.ProofPS:
				line += fmt.Sprintf("; allocated %d, confirmed by /api/ps", r.ProvenCtx)
			case llm.ProofProbe:
				line += fmt.Sprintf("; at least %d proven by probe", r.ProvenCtx)
				if r.ProofCached {
					line += " (cached)"
				}
			}
			fmt.Println(line)
		}
		if r.EmbeddingDim > 0 {
			fmt.Printf("       %d dimensions\n", r.EmbeddingDim)
		}
		for _, ok := range r.OK {
			fmt.Printf("       · %s\n", wrap(ok, 9))
		}
		for _, p := range r.Problems {
			fmt.Printf("       ✗ %s\n", wrap(p, 9))
		}
	}
	fmt.Println()
}

// wrap indents continuation lines so a long, teaching error message stays
// readable in a terminal.
func wrap(s string, indent int) string {
	const width = 92
	pad := strings.Repeat(" ", indent)
	var out strings.Builder
	col := indent
	for i, word := range strings.Fields(s) {
		if i > 0 {
			if col+1+len(word) > width {
				out.WriteString("\n" + pad)
				col = indent
			} else {
				out.WriteByte(' ')
				col++
			}
		}
		out.WriteString(word)
		col += len(word)
	}
	return out.String()
}

// ── openapi ──────────────────────────────────────────────────────────────────

func cmdOpenAPI(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("openapi", flag.ContinueOnError)
	asYAML := fs.Bool("yaml", false, "emit YAML instead of JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Deliberately no database and no model server: the document describes the
	// API's shape, and needing infrastructure to print it would make it
	// impossible to generate the frontend client in CI.
	srv := httpapi.New(httpapi.Deps{Log: slog.New(slog.DiscardHandler)})

	var (
		out []byte
		err error
	)
	if *asYAML {
		out, err = srv.OpenAPI().YAML()
	} else {
		out, err = srv.OpenAPI().MarshalJSON()
	}
	if err != nil {
		return fmt.Errorf("emitting the OpenAPI document: %w", err)
	}
	_, err = os.Stdout.Write(append(out, '\n'))
	return err
}
