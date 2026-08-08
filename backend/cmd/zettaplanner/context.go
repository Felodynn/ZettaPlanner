package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/config"
	"github.com/Felodynn/ZettaPlanner/backend/internal/contextbuild"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/contextsvc"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// cmdContext is the most valuable debugging tool in this build.
//
// Every "why did the agent say that" question starts here, and the answer is
// almost always visible in the assembled text: something essential is missing,
// something misleading is present, or the expansion resolved to the wrong item.
// So it prints exactly what a turn would receive — the same bytes, not a
// summary — with a per-layer accounting above it and what was dropped below.
func cmdContext(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("context needs a subcommand: show")
	}
	switch args[0] {
	case "show":
		return cmdContextShow(ctx, args[1:])
	default:
		return fmt.Errorf("unknown context subcommand %q; use show", args[0])
	}
}

func cmdContextShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("context show", flag.ContinueOnError)
	project := fs.String("project", "", "project key (ZP) or id")
	message := fs.String("message", "", "the turn's prompt; expansion starts from what it names")
	item := fs.String("item", "", "pull one work item in explicitly, by ref (ZP-14) or number")
	model := fs.String("model", "", "which model's assembly_budget to target (default: the planner role's first choice)")
	tokens := fs.Bool("tokens", false, "print the per-layer accounting and stop, without the context itself")
	as := fs.String("as", "", "assemble as this user's email (default: the first admin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *project == "" {
		return errors.New("context show needs -project (a key like ZP, or an id)")
	}
	if *message == "" && *item == "" {
		return errors.New("context show needs -message or -item; with neither there is nothing for expansion to start from")
	}

	cfg, log, err := boot()
	if err != nil {
		return err
	}
	// The command is read-only and prints its own output. Server logs
	// interleaved with the context would make the thing being read unreadable.
	quiet := slog.New(slog.DiscardHandler)

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx = telemetry.WithRequestID(ctx, telemetry.NewRequestID())

	// Model validation is skipped deliberately: `context show` must work when
	// the model server is down, because "the model server is down" is one of
	// the things it is used to diagnose. Retrieval then reports itself
	// unavailable in the output, which is the correct answer rather than a
	// crash.
	registry, err := loadRegistryQuietly(cfg.ModelsPath)
	if err != nil {
		log.WarnContext(ctx, "no model registry; retrieval will report itself unavailable", "error", err)
	}

	svc := contextsvc.New(db, quiet, registry)

	projectID, key, err := resolveProject(ctx, db, *project)
	if err != nil {
		return err
	}
	principal, err := resolvePrincipal(ctx, db, *as)
	if err != nil {
		return err
	}

	res, err := svc.Assemble(ctx, projectID, contextsvc.Request{
		Message: *message,
		ItemRef: *item,
		Model:   *model,
	}, principal)
	if err != nil {
		return err
	}

	printAccounting(res, key)
	if *tokens {
		return nil
	}
	fmt.Println()
	fmt.Println(strings.Repeat("─", 78))
	fmt.Print(res.Text)
	fmt.Println(strings.Repeat("─", 78))
	return nil
}

// printAccounting is the part that answers "why is the context so large".
func printAccounting(res *contextsvc.Result, key string) {
	c := res.Context
	fmt.Printf("project:   %s\n", key)
	fmt.Printf("budget:    %d tokens against %s\n", c.Budget.Tokens, c.Budget.Model)
	fmt.Printf("assembled: ~%d tokens (estimated; not a tokenizer)\n", c.Tokens)
	if c.OverBudget() {
		fmt.Printf("           ⚠ OVER BUDGET by ~%d tokens even after dropping everything droppable.\n",
			c.Tokens-c.Budget.Tokens)
		fmt.Printf("             What is left is the part that is never cut: the project, its open\n")
		fmt.Printf("             deviations, and what has already been rejected.\n")
	}

	fmt.Println()
	fmt.Println("layers:")
	for _, layer := range []contextbuild.Layer{contextbuild.LayerSkeleton, contextbuild.LayerExpansion, contextbuild.LayerSpill} {
		n := c.ByLayer[layer]
		share := 0
		if c.Tokens > 0 {
			share = n * 100 / c.Tokens
		}
		fmt.Printf("  %-10s ~%6d tokens  %3d%%\n", layer, n, share)
	}

	fmt.Println()
	fmt.Println("sections:")
	for _, s := range c.Sections {
		fmt.Printf("  %-10s %-22s ~%6d tokens\n", s.Layer, s.Title, s.Tokens)
	}

	// What the message resolved to. "The context is about the wrong item" is a
	// failure that looks exactly like "the model is bad at this", and this line
	// is what tells them apart.
	fmt.Println()
	fmt.Println("expansion started from:")
	if res.References.Empty() {
		fmt.Println("  (nothing — the message named no work item ref and no known entity)")
	} else {
		if len(res.References.ItemRefs) > 0 {
			fmt.Printf("  work items: %s\n", strings.Join(res.References.ItemRefs, ", "))
		}
		if len(res.References.EntitySlugs) > 0 {
			fmt.Printf("  entities:   %s\n", strings.Join(res.References.EntitySlugs, ", "))
		}
	}

	if len(c.Drops) > 0 {
		fmt.Println()
		fmt.Println("omitted:")
		for _, d := range c.Drops {
			fmt.Printf("  [%s] %s — %s\n", d.Layer, d.What, d.Why)
		}
	}
}

// resolveProject accepts a key or an id, because nobody remembers a UUID and
// everybody knows their project key.
func resolveProject(ctx context.Context, db *store.DB, ref string) (uuid.UUID, string, error) {
	var projects repo.Projects
	if id, err := uuid.Parse(ref); err == nil {
		p, err := projects.ByID(ctx, db.Pool, id)
		if err != nil {
			return uuid.Nil, "", err
		}
		return p.ID, p.Key, nil
	}
	p, err := projects.ByKey(ctx, db.Pool, strings.ToUpper(ref))
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("no project with key %q: %w", ref, err)
	}
	return p.ID, p.Key, nil
}

// resolvePrincipal picks whose eyes to assemble through.
//
// It matters: membership decides whether the project is visible at all, and
// assembling as an admin would hide a permissions problem that a real turn
// would hit. Defaults to the first admin because that is who is running the
// command at 11pm.
func resolvePrincipal(ctx context.Context, db *store.DB, email string) (policy.Principal, error) {
	var (
		users  repo.Users
		actors repo.Actors
	)

	if email != "" {
		u, err := users.ByEmail(ctx, db.Pool, email)
		if err != nil {
			return policy.Principal{}, err
		}
		actor, err := actors.ForUser(ctx, db.Pool, u.ID)
		if err != nil {
			return policy.Principal{}, err
		}
		return policy.Principal{
			UserID: u.ID, ActorID: actor.ID, Email: u.Email,
			DisplayName: u.DisplayName, GlobalRole: u.Role, IsActive: u.IsActive,
		}, nil
	}

	all, err := users.List(ctx, db.Pool)
	if err != nil {
		return policy.Principal{}, err
	}
	for _, u := range all {
		if u.Role != policy.RoleAdmin || !u.IsActive {
			continue
		}
		actor, err := actors.ForUser(ctx, db.Pool, u.ID)
		if err != nil {
			return policy.Principal{}, err
		}
		return policy.Principal{
			UserID: u.ID, ActorID: actor.ID, Email: u.Email,
			DisplayName: u.DisplayName, GlobalRole: u.Role, IsActive: u.IsActive,
		}, nil
	}
	return policy.Principal{}, errors.New("this deployment has no active admin; run `zettaplanner admin create-user`")
}

// loadRegistryQuietly builds the model registry **without running validation**.
//
// Deliberate: `context show` has to work when the model server is down, because
// that is one of the things it is used to diagnose. Retrieval then reports
// itself unavailable in the output — which is the answer — instead of the
// command refusing to start.
func loadRegistryQuietly(path string) (*llm.Registry, error) {
	models, err := config.LoadModels(path)
	if err != nil {
		return nil, err
	}
	return llm.NewRegistry(models), nil
}
