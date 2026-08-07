// Package telemetry configures structured logging and threads a request id
// through context.Context.
//
// The request id is not a logging nicety. Every audit row carries it as a
// NOT NULL column (CONTRACT §6.5 constraint 3), and it is what distinguishes
// "an agent applied a 14-change proposal in one operation" from "someone made
// 14 edits over an afternoon". Generating it in middleware and reading it back
// out of the context is the only path; no handler invents its own.
package telemetry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
)

type ctxKey int

const requestIDKey ctxKey = iota

// NewRequestID returns a fresh, time-sortable request id.
func NewRequestID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// WithRequestID returns a context carrying id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID returns the request id carried by ctx, or "" if there is none.
// A "" here on a write path is a bug: the audit insert will fail the NOT NULL.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// contextHandler copies context-scoped fields onto every record so that no
// call site has to remember to attach them.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := RequestID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return contextHandler{Handler: h.Handler.WithAttrs(as)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{Handler: h.Handler.WithGroup(name)}
}

// Options configures the process logger.
type Options struct {
	Level  string // debug | info | warn | error
	Format string // json | text
	Output io.Writer
}

// Setup builds the process logger and installs it as the slog default.
// Use the ...Context variants (slog.InfoContext, …) everywhere so the request
// id is picked up automatically.
func Setup(opts Options) (*slog.Logger, error) {
	var level slog.Level
	switch strings.ToLower(opts.Level) {
	case "", "info":
		level = slog.LevelInfo
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("log level %q is not one of debug, info, warn, error", opts.Level)
	}

	out := opts.Output
	if out == nil {
		out = os.Stderr
	}

	hopts := &slog.HandlerOptions{Level: level}
	var base slog.Handler
	switch strings.ToLower(opts.Format) {
	case "", "json":
		base = slog.NewJSONHandler(out, hopts)
	case "text":
		base = slog.NewTextHandler(out, hopts)
	default:
		return nil, fmt.Errorf("log format %q is not one of json, text", opts.Format)
	}

	logger := slog.New(contextHandler{Handler: base})
	slog.SetDefault(logger)
	return logger, nil
}
