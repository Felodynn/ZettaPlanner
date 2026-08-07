// Command zettaplanner is the single binary: HTTP API, migrations, model
// validation, and the operator tooling for all of it.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintf(os.Stderr, "\nzettaplanner: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("no command given")
	}

	switch args[0] {
	case "serve":
		return cmdServe(ctx, args[1:])
	case "migrate":
		return cmdMigrate(ctx, args[1:])
	case "models":
		return cmdModels(ctx, args[1:])
	case "openapi":
		return cmdOpenAPI(ctx, args[1:])
	case "admin":
		return cmdAdmin(ctx, args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `zettaplanner — AI-native technical planning substrate

Usage:
  zettaplanner serve                 Run the HTTP API
  zettaplanner migrate up            Apply pending migrations
  zettaplanner migrate down          Revert the most recent migration
  zettaplanner migrate status        Show the current schema version
  zettaplanner models check          Validate every configured model
  zettaplanner openapi               Emit the OpenAPI 3.1 document
  zettaplanner admin create-user     Create the first account (bootstrap)

Environment:
  ZP_ADDR                  Listen address (default :8080)
  ZP_DATABASE_URL          libpq connection string (default postgres:///zettaplanner)
  ZP_MODELS_PATH           Model registry (default ~/.zettaplanner/models.yaml)
  ZP_UPLOADS_DIR           Ingest source images (default data/uploads)
  ZP_LOG_LEVEL             debug | info | warn | error (default info)
  ZP_LOG_FORMAT            text | json (default text)
  ZP_SESSION_TTL           Session lifetime (default 336h, fourteen days)
  ZP_SECURE_COOKIES        Mark the session cookie Secure. On for any TLS deployment.
  ZP_TRUST_PROXY           Trust X-Forwarded-For for the audited client address
  ZP_SKIP_MODEL_VALIDATION Skip the live model checks at boot. Never in a deployment.

`)
}
