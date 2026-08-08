package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/gitproj"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/gitexport"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/project"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// cmdGit is the operator surface of the one-way projection (D10).
//
// Deliberately an operator command rather than an API route: turning the
// projection on decides that a directory on this machine's disk gets written
// to, which is a deployment decision and not something a project manager makes
// from a browser. The flag itself lives on the project row, so the API can
// report whether it is on — it just cannot be the thing that chooses the path.
func cmdGit(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("git needs a subcommand: enable, disable, resync, or status")
	}

	fs := flag.NewFlagSet("git "+args[0], flag.ContinueOnError)
	projectRef := fs.String("project", "", "project key or id")
	as := fs.String("as", "", "act as this user's email (defaults to the first admin)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *projectRef == "" {
		return errors.New("which project? pass --project with a key such as ZP")
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

	projectID, key, err := resolveProject(ctx, db, *projectRef)
	if err != nil {
		return err
	}
	svc := gitexport.New(db, log, cfg.GitDir, cfg.GitPoll)

	switch args[0] {
	case "enable", "disable":
		on := args[0] == "enable"
		if on {
			// Checked before the flag is written rather than after: a project
			// marked enabled whose projection can never run is a lie the health
			// check has no way to notice.
			if err := gitproj.Available(); err != nil {
				return err
			}
		}
		principal, err := resolvePrincipal(ctx, db, *as)
		if err != nil {
			return err
		}
		if _, err := project.New(db, log).Update(ctx, projectID,
			project.UpdateInput{GitEnabled: &on}, principal); err != nil {
			return err
		}
		if !on {
			fmt.Printf("git projection off for %s. The directory is left where it is: it is disposable, "+
				"not secret, and deleting somebody's tree on their behalf is not this command's decision.\n", key)
			return nil
		}
		fmt.Printf("git projection on for %s → %s\n", key, svc.DirFor(key))
		fmt.Println("Projecting now.")
		return report(ctx, svc, projectID, true)

	case "resync":
		// force, so it works whether or not the flag is on and whether or not
		// anything has changed. Resync is what somebody runs when they do not
		// trust the tree, and a resync that decides nothing needs doing answers
		// a question nobody asked.
		return report(ctx, svc, projectID, true)

	case "status":
		return gitStatus(ctx, svc, key)

	default:
		return fmt.Errorf("unknown git subcommand %q; use enable, disable, resync, or status", args[0])
	}
}

func report(ctx context.Context, svc *gitexport.Service, projectID uuid.UUID, force bool) error {
	rep, err := svc.Sync(ctx, projectID, force)
	if err != nil {
		return err
	}
	if rep.Skipped {
		fmt.Printf("%s: the projection is off. Turn it on with `zettaplanner git enable --project %s`.\n",
			rep.ProjectKey, rep.ProjectKey)
		return nil
	}
	fmt.Printf("%s → %s\n", rep.ProjectKey, rep.Dir)
	fmt.Printf("  %d files\n", rep.Files)
	if !rep.Changed {
		fmt.Println("  nothing to commit; the tree already matches the database")
		return nil
	}
	fmt.Printf("  committed %s as %s <%s>\n", rep.Commit, rep.Identity.Name, rep.Identity.Email)
	fmt.Printf("  tree %s\n", rep.Tree)
	return nil
}

func gitStatus(ctx context.Context, svc *gitexport.Service, key string) error {
	dir := svc.DirFor(key)
	if _, err := os.Stat(dir); err != nil {
		fmt.Printf("%s: no projection directory at %s yet.\n", key, dir)
		return nil
	}
	r, err := gitproj.Open(ctx, dir)
	if err != nil {
		return err
	}
	tree, err := r.TreeHash(ctx)
	if err != nil {
		fmt.Printf("%s: %s exists but has no commits yet.\n", key, dir)
		return nil
	}
	fmt.Printf("%s → %s\n", key, dir)
	fmt.Printf("  tree %s\n", tree)
	if wm, ok := r.Watermark(); ok {
		fmt.Printf("  projected through audit entry %s\n", wm)
	} else {
		fmt.Println("  no watermark; the next sync rebuilds the whole tree and commits only if it differs")
	}
	log, err := r.Log(ctx, 10)
	if err != nil {
		return err
	}
	for _, line := range log {
		fmt.Println("  " + line)
	}
	return nil
}
