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
	"time"
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

	// SessionTTL is how long a session lives without being renewed.
	SessionTTL time.Duration

	// LeaseTTL is how long a diagram edit session survives without a
	// heartbeat (Q6). Long enough to outlast a screen lock or a network blip,
	// short enough that a browser killed mid-edit does not hold a diagram for
	// an afternoon.
	LeaseTTL time.Duration

	// SecureCookies marks the session cookie Secure. Off by default because
	// development runs on plain http; a cookie the browser refuses to store is
	// a login that silently does nothing. On for any TLS deployment.
	SecureCookies bool

	// TrustProxy makes X-Forwarded-For authoritative for the client address
	// recorded in the audit log. Only true when something actually terminates
	// in front of this process.
	TrustProxy bool

	// SkipModelValidation bypasses the live model checks at boot. It exists
	// for migrations and offline tooling, never for `serve`.
	SkipModelValidation bool

	// GitDir is the parent directory of the one-way git projections, one
	// repository per project (D10). Off unless a project turns it on; the
	// directory is disposable and regenerable from the database.
	GitDir string

	// GitPoll is how often the projection worker looks for something to
	// project. A poll rather than a hook: the projection must never sit inside
	// the transaction of a write, so the price of that is being up to one
	// interval stale, which is what D10 says it is allowed to be.
	GitPoll time.Duration

	// DevOrigin allows one browser origin to call this API cross-site with
	// credentials, for a frontend dev server.
	//
	// Empty in every deployment: the SPA is served by this binary (D2), so the
	// production answer is same-origin and no CORS at all. It exists because a
	// Vite dev server on :5173 cannot hold a SameSite=Lax session cookie
	// otherwise, and the alternative — developing against a proxy that hides
	// the cookie rules — means discovering them in production.
	DevOrigin string
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
		SecureCookies:       envBool("ZP_SECURE_COOKIES", false),
		TrustProxy:          envBool("ZP_TRUST_PROXY", false),
		GitDir:              env("ZP_GIT_DIR", "data/git"),
		DevOrigin:           strings.TrimRight(env("ZP_DEV_ORIGIN", ""), "/"),
	}

	ttl, err := time.ParseDuration(env("ZP_SESSION_TTL", "336h"))
	if err != nil {
		return nil, fmt.Errorf("ZP_SESSION_TTL is not a duration (try \"336h\" for 14 days): %w", err)
	}
	c.SessionTTL = ttl

	leaseTTL, err := time.ParseDuration(env("ZP_LEASE_TTL", "5m"))
	if err != nil {
		return nil, fmt.Errorf("ZP_LEASE_TTL is not a duration (try \"5m\"): %w", err)
	}
	c.LeaseTTL = leaseTTL

	gitPoll, err := time.ParseDuration(env("ZP_GIT_POLL", "60s"))
	if err != nil {
		return nil, fmt.Errorf("ZP_GIT_POLL is not a duration (try \"60s\"): %w", err)
	}
	c.GitPoll = gitPoll

	if c.DatabaseURL == "" {
		return nil, fmt.Errorf("ZP_DATABASE_URL is empty; set it to something like postgres:///zettaplanner or postgres://user:pass@host:5432/zettaplanner")
	}
	if abs, err := filepath.Abs(c.UploadsDir); err == nil {
		c.UploadsDir = abs
	}
	if abs, err := filepath.Abs(c.GitDir); err == nil {
		c.GitDir = abs
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
