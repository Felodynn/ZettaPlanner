package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/auth"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

func cmdAdmin(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("admin needs a subcommand: create-user")
	}
	switch args[0] {
	case "create-user":
		return cmdCreateUser(ctx, args[1:])
	default:
		return fmt.Errorf("unknown admin subcommand %q; use create-user", args[0])
	}
}

// cmdCreateUser bootstraps the first account.
//
// Without it there is no admin, and without an admin there is no way to make
// one. The service refuses the moment any account exists, so this cannot become
// a back door once the deployment is running.
func cmdCreateUser(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("admin create-user", flag.ContinueOnError)
	email := fs.String("email", "", "email address")
	name := fs.String("name", "", "display name")
	role := fs.String("role", string(policy.RoleAdmin), "admin | manager | developer")
	// Deliberately no -password flag. A password passed as an argument lands in
	// the shell history and in the process list, where it outlives the command
	// by a long way.
	stdinPassword := fs.Bool("password-stdin", false, "read the password from stdin instead of prompting")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" || *name == "" {
		return errors.New("admin create-user needs -email and -name")
	}
	parsedRole, err := policy.ParseRole(*role)
	if err != nil {
		return err
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

	version, err := store.SchemaVersion(ctx, db.Pool)
	if err != nil || version == 0 {
		return errors.New("the database has no schema; run `zettaplanner migrate up` first")
	}

	password, err := readPassword(*stdinPassword)
	if err != nil {
		return err
	}

	svc := auth.New(db, log, cfg.SessionTTL)
	in := auth.CreateUserInput{
		Email:       *email,
		DisplayName: *name,
		Password:    password,
		Role:        parsedRole,
	}

	user, err := svc.Bootstrap(ctx, in)
	if err != nil {
		return err
	}
	fmt.Printf("created %s (%s) as %s\n", user.Email, user.ID, user.Role)
	return nil
}

func readPassword(fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("reading the password from stdin: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("stdin is not a terminal; pass -password-stdin and pipe the password in")
	}

	fmt.Print("password: ")
	first, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	fmt.Print("again: ")
	second, err := term.ReadPassword(fd)
	fmt.Println()
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("the two passwords do not match")
	}
	return string(first), nil
}
