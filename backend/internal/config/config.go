// Package config loads the server configuration and the model registry.
//
// Everything here is validated at startup and fails loudly with a message that
// names the fix. A configuration error discovered at the first request is a
// configuration error discovered by a user.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the process configuration, read from the environment.
type Config struct {
	// Addr is the listen address for the HTTP server.
	Addr string

	// DatabaseURL is a libpq connection string. The default uses the Unix
	// socket and peer authentication, which is what a local development
	// Postgres gives you without a password.
	DatabaseURL string

	// ModelsPath points at models.yaml. Machine-scoped, never committed
	// (CONTRACT §8).
	ModelsPath string

	// UploadsDir holds ingest source images, referenced by path from the
	// ingests tables (CONTRACT §6.1).
	UploadsDir string

	LogLevel  string
	LogFormat string

	// SkipModelValidation bypasses the live model checks at boot. It exists
	// for migrations and offline tooling, never for `serve`.
	SkipModelValidation bool
}

// Load reads configuration from the environment, applying defaults.
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home directory for the default models.yaml path: %w", err)
	}

	c := &Config{
		Addr:                env("ZP_ADDR", ":8080"),
		DatabaseURL:         env("ZP_DATABASE_URL", "postgres:///zettaplanner"),
		ModelsPath:          env("ZP_MODELS_PATH", filepath.Join(home, ".zettaplanner", "models.yaml")),
		UploadsDir:          env("ZP_UPLOADS_DIR", "data/uploads"),
		LogLevel:            env("ZP_LOG_LEVEL", "info"),
		LogFormat:           env("ZP_LOG_FORMAT", "text"),
		SkipModelValidation: envBool("ZP_SKIP_MODEL_VALIDATION", false),
	}

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("ZP_DATABASE_URL is empty; set it to something like postgres:///zettaplanner or postgres://user:pass@host:5432/zettaplanner")
	}
	if abs, err := filepath.Abs(c.UploadsDir); err == nil {
		c.UploadsDir = abs
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
