package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Class is the normalised failure class of a model call.
//
// Local Ollama and Ollama Cloud return different shapes for the same condition,
// and the agent loop's retry policy is keyed on this enum rather than on
// whatever string the server chose today. Only the classes marked Retryable
// here are ever retried (BACKEND.md Phase 6); a boundary or validation failure
// retried is a refusal rephrased, not a problem solved.
type Class string

const (
	// ClassContextOverflow — the prompt exceeded the model's window. Not
	// retryable: the same prompt overflows the same window every time.
	ClassContextOverflow Class = "context_overflow"

	// ClassToolParse — the model returned something that is not a usable tool
	// call or is not parseable at all, including an empty content field.
	// Retryable once. This is the class that killed two of five golden-task
	// attempts in LumixAgents Phase 6 without ever being retried.
	ClassToolParse Class = "tool_parse"

	// ClassRateLimit — the provider refused for quota reasons. Not retried
	// in-place; it ends the turn and the next turn resolves to the next model
	// in the role's prefer order (D22).
	ClassRateLimit Class = "rate_limit"

	// ClassModelUnavailable — the model is not loaded, not pulled, or the
	// server is unreachable. Not retryable: it needs an operator.
	ClassModelUnavailable Class = "model_unavailable"

	// ClassTimeout — the call exceeded its deadline. Retryable once.
	ClassTimeout Class = "timeout"

	// ClassServer — a 5xx or a transport failure. Retryable once.
	ClassServer Class = "server"

	// ClassInvalidRequest — we sent something wrong. Not retryable; it is a
	// bug in this codebase and must surface as one.
	ClassInvalidRequest Class = "invalid_request"
)

// Retryable reports whether the agent loop may retry this class exactly once.
func (c Class) Retryable() bool {
	switch c {
	case ClassToolParse, ClassServer, ClassTimeout:
		return true
	}
	return false
}

// Sentinels for errors.Is. The concrete error is *Error; these identify the
// class without unwrapping.
var (
	ErrContextOverflow  = errors.New("context overflow")
	ErrToolParse        = errors.New("tool parse")
	ErrRateLimit        = errors.New("rate limit")
	ErrModelUnavailable = errors.New("model unavailable")
	ErrTimeout          = errors.New("timeout")
	ErrServer           = errors.New("model server error")
	ErrInvalidRequest   = errors.New("invalid request")
)

var sentinels = map[Class]error{
	ClassContextOverflow:  ErrContextOverflow,
	ClassToolParse:        ErrToolParse,
	ClassRateLimit:        ErrRateLimit,
	ClassModelUnavailable: ErrModelUnavailable,
	ClassTimeout:          ErrTimeout,
	ClassServer:           ErrServer,
	ClassInvalidRequest:   ErrInvalidRequest,
}

// Error is a normalised model failure. It carries enough to be recorded on
// agent_turns.error_class and replayed by `zettaplanner trace`.
type Error struct {
	Class    Class
	Registry string // registry key, e.g. "planner-cloud"
	Model    string // Ollama tag, e.g. "glm-5.2:cloud"
	Endpoint string
	Status   int    // HTTP status, 0 for transport failures
	Detail   string // the server's own words, kept verbatim
	cause    error
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: model %q", e.Class, e.Model)
	if e.Registry != "" {
		fmt.Fprintf(&b, " (%s)", e.Registry)
	}
	if e.Endpoint != "" {
		fmt.Fprintf(&b, " at %s", e.Endpoint)
	}
	if e.Status != 0 {
		fmt.Fprintf(&b, ": HTTP %d", e.Status)
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, ": %s", e.Detail)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.cause }

// Is lets errors.Is(err, ErrTimeout) work against the class sentinels.
func (e *Error) Is(target error) bool {
	if s, ok := sentinels[e.Class]; ok && errors.Is(s, target) {
		return true
	}
	return false
}

// Retryable reports whether the agent loop may retry this once.
func (e *Error) Retryable() bool { return e.Class.Retryable() }

// ClassOf returns the normalised class of any error produced by this package,
// or "" if err did not come from here.
func ClassOf(err error) Class {
	var e *Error
	if errors.As(err, &e) {
		return e.Class
	}
	return ""
}

// normalise maps a transport failure or a non-2xx Ollama response onto a Class.
//
// The substring matching is deliberate and load-bearing: Ollama does not
// return machine-readable error codes, and the alternative is for every caller
// to grep the message itself. When a new phrasing appears, it is added here and
// nowhere else.
func normalise(ctx context.Context, m modelRef, status int, body string, cause error) *Error {
	e := &Error{
		Registry: m.registry,
		Model:    m.model,
		Endpoint: m.endpoint,
		Status:   status,
		Detail:   strings.TrimSpace(body),
		cause:    cause,
	}

	// Deadlines first: a cancelled context outranks whatever the transport
	// reported on its way down.
	if cause != nil {
		switch {
		case errors.Is(cause, context.DeadlineExceeded):
			e.Class = ClassTimeout
			if e.Detail == "" {
				e.Detail = "deadline exceeded"
			}
			return e
		case errors.Is(cause, context.Canceled):
			// Cancellation is the caller's decision, not a model failure, but
			// it still has to carry a class for the turn record.
			e.Class = ClassTimeout
			if e.Detail == "" {
				e.Detail = "cancelled"
			}
			return e
		}
		var nerr net.Error
		if errors.As(cause, &nerr) && nerr.Timeout() {
			e.Class = ClassTimeout
			return e
		}
		var opErr *net.OpError
		if errors.As(cause, &opErr) || strings.Contains(cause.Error(), "connection refused") {
			e.Class = ClassModelUnavailable
			if e.Detail == "" {
				e.Detail = cause.Error()
			}
			e.Detail += " — is Ollama running at " + m.endpoint + "?"
			return e
		}
	}

	lower := strings.ToLower(body)
	switch {
	case strings.Contains(lower, "context length") ||
		strings.Contains(lower, "context window") ||
		strings.Contains(lower, "too many tokens") ||
		strings.Contains(lower, "exceeds the context") ||
		strings.Contains(lower, "maximum context"):
		e.Class = ClassContextOverflow
		return e

	case status == http.StatusTooManyRequests ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "capacity"):
		e.Class = ClassRateLimit
		return e

	case status == http.StatusNotFound ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "try pulling it first") ||
		strings.Contains(lower, "no such model"):
		e.Class = ClassModelUnavailable
		if !strings.Contains(lower, "pull") {
			e.Detail += fmt.Sprintf(" — run `ollama pull %s`", m.model)
		}
		return e

	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.Class = ClassModelUnavailable
		e.Detail += " — the endpoint rejected our credentials; check `ollama signin` or the model's auth_env"
		return e
	}

	switch {
	case status >= 500:
		e.Class = ClassServer
	case status >= 400:
		e.Class = ClassInvalidRequest
	case status == 0 && cause != nil:
		e.Class = ClassServer
	default:
		e.Class = ClassServer
	}
	return e
}

// toolParseError reports model output that could not be turned into a usable
// call. Separate constructor because it has no HTTP status behind it: the
// request succeeded and the content was unusable.
func toolParseError(m modelRef, detail string, cause error) *Error {
	return &Error{
		Class:    ClassToolParse,
		Registry: m.registry,
		Model:    m.model,
		Endpoint: m.endpoint,
		Detail:   detail,
		cause:    cause,
	}
}

// modelRef is the identifying trio carried into every error.
type modelRef struct {
	registry string
	model    string
	endpoint string
}
