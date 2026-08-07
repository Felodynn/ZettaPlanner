package httpapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// Code is the closed set of error codes from CONTRACT §7.3. Every failure that
// crosses the API boundary carries one of these; the frontend switches on it.
type Code string

const (
	CodeUnauthenticated   Code = "unauthenticated"
	CodeForbidden         Code = "forbidden"
	CodeFieldNotOwned     Code = "field_not_owned"
	CodeNotFound          Code = "not_found"
	CodeConflict          Code = "conflict"
	CodeLeaseHeld         Code = "lease_held"
	CodeStaleBaseRevision Code = "stale_base_revision"
	CodeValidationFailed  Code = "validation_failed"
	CodeTurnInProgress    Code = "turn_in_progress"
	CodeModelUnavailable  Code = "model_unavailable"
	CodeContextOverflow   Code = "context_overflow"
	CodeInternal          Code = "internal"
)

// codeEnum is the OpenAPI enum for Code. Kept adjacent to the constants so a
// new code that is not published is visible as an omission.
const codeEnum = "unauthenticated,forbidden,field_not_owned,not_found,conflict," +
	"lease_held,stale_base_revision,validation_failed,turn_in_progress," +
	"model_unavailable,context_overflow,internal"

// Codes returns every error code.
func Codes() []Code {
	return []Code{
		CodeUnauthenticated, CodeForbidden, CodeFieldNotOwned, CodeNotFound,
		CodeConflict, CodeLeaseHeld, CodeStaleBaseRevision, CodeValidationFailed,
		CodeTurnInProgress, CodeModelUnavailable, CodeContextOverflow, CodeInternal,
	}
}

// Status is the HTTP status this code is served with.
func (c Code) Status() int {
	switch c {
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodeForbidden, CodeFieldNotOwned:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict, CodeLeaseHeld, CodeStaleBaseRevision, CodeTurnInProgress:
		return http.StatusConflict
	case CodeValidationFailed:
		return http.StatusBadRequest
	case CodeModelUnavailable:
		return http.StatusServiceUnavailable
	case CodeContextOverflow:
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

// ErrorBody is the inner object of the envelope.
type ErrorBody struct {
	Code    Code   `json:"code" enum:"unauthenticated,forbidden,field_not_owned,not_found,conflict,lease_held,stale_base_revision,validation_failed,turn_in_progress,model_unavailable,context_overflow,internal" doc:"Machine-readable error code"`
	Message string `json:"message" doc:"Human-readable explanation. For a denial this is prompt text: it leads with the route forward and names the owning role."`
	// Details is structured detail specific to the code — the field and its
	// owning role for field_not_owned, the conflicting change for
	// stale_base_revision, the valid values for validation_failed.
	Details   map[string]any `json:"details,omitempty" doc:"Structured detail specific to the code"`
	RequestID string         `json:"request_id" doc:"Correlates this failure with the audit log and the server logs"`
}

// Error is the one error shape, always (CONTRACT §7.3).
//
// The envelope is the Go type rather than something produced by a custom
// MarshalJSON, because huma derives the OpenAPI error schema by reflecting over
// whatever huma.NewError returns. A hand-rolled marshaller would emit the
// contract's shape at runtime while the document advertised a different one,
// and the frontend's client is generated from that document — the generated lie
// D2 exists to prevent, one level down.
type Error struct {
	Err ErrorBody `json:"error"`

	status int
	cause  error
}

// Errorf builds an API error.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{
		Err:    ErrorBody{Code: code, Message: fmt.Sprintf(format, args...)},
		status: code.Status(),
	}
}

// Code returns the error's code.
func (e *Error) Code() Code { return e.Err.Code }

// Message returns the human-readable explanation.
func (e *Error) Message() string { return e.Err.Message }

// WithDetail attaches one structured detail field.
func (e *Error) WithDetail(key string, value any) *Error {
	if e.Err.Details == nil {
		e.Err.Details = map[string]any{}
	}
	e.Err.Details[key] = value
	return e
}

// WithCause attaches the underlying error for logging. It is never serialised:
// a client learns the code and the message, never the internals.
func (e *Error) WithCause(err error) *Error {
	e.cause = err
	return e
}

// WithRequestID stamps the correlation id. Normally done by the huma hook, so
// handlers do not have to remember.
func (e *Error) WithRequestID(id string) *Error {
	e.Err.RequestID = id
	return e
}

// WithStatus overrides the status derived from the code. Used only when huma
// raises a status this package has no code for.
func (e *Error) WithStatus(status int) *Error {
	e.status = status
	return e
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Err.Code, e.Err.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Err.Code, e.Err.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// GetStatus satisfies huma.StatusError.
func (e *Error) GetStatus() int {
	if e.status != 0 {
		return e.status
	}
	return e.Err.Code.Status()
}

// ── constructors for the codes with prescribed wording ───────────────────────

// Internal wraps an unexpected failure. The cause is logged, never returned.
func Internal(ctx context.Context, cause error) *Error {
	return Errorf(CodeInternal, "Something failed on our side. Quote the request id when reporting it.").
		WithCause(cause).
		WithRequestID(telemetry.RequestID(ctx))
}

// NotFound reports a missing subject.
func NotFound(kind, id string) *Error {
	return Errorf(CodeNotFound, "No %s with id %s.", kind, id).
		WithDetail("kind", kind).
		WithDetail("id", id)
}

// ValidationFailed reports a bad request. The message must teach: it is read by
// the agent in Phase 6 as well as by a human, so it names the valid values.
func ValidationFailed(format string, args ...any) *Error {
	return Errorf(CodeValidationFailed, format, args...)
}

// FieldNotOwned is the canonical denial (CONTRACT §7.3, §4.2).
//
// Denial text is a prompt layer. LumixAgents D34 measured it: a refusal that
// leads with the reason gets prose back, and a refusal that leads with the
// action gets the action. So the message leads with the route forward and then
// names the owning role — for a human reading a toast and a model reading a
// tool result alike.
func FieldNotOwned(field, owningRole, effectiveRole, route string) *Error {
	return Errorf(CodeFieldNotOwned,
		"To change %s, %s. It is %s-owned, and your effective role on this project is %s.",
		field, route, owningRole, effectiveRole).
		WithDetail("field", field).
		WithDetail("owning_role", owningRole).
		WithDetail("effective_role", effectiveRole).
		WithDetail("route", route)
}

// Forbidden reports a denial that is not about a specific field. It still names
// the route forward.
func Forbidden(action, route string) *Error {
	return Errorf(CodeForbidden, "To %s, %s.", action, route).
		WithDetail("route", route)
}
