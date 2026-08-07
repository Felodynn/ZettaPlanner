package httpapi

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/domain/ingest"
	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/repo"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
)

// revisionSummaries renders the revisions that landed under a caller, so a
// conflict response says what changed rather than only that something did.
func revisionSummaries(list []*repo.Revision) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, r := range list {
		out = append(out, map[string]any{
			"seq": r.Seq, "summary": r.Summary, "changes": r.Changes, "at": r.CreatedAt,
		})
	}
	return out
}

// mapError turns anything the layers below return into the contract envelope.
//
// This is the one place that mapping happens (BACKEND.md §1.2). Every handler
// funnels its error through here, so a new sentinel is wired once rather than
// wherever it happens to surface — and no service ever imports net/http to
// decide what a failure means.
func mapError(ctx context.Context, log *slog.Logger, err error) error {
	if err == nil {
		return nil
	}

	// A policy denial already carries its message, its route and its details.
	// It is prompt text (§7.3): it is returned verbatim to a human in a 403 and
	// to an agent as a tool result, so nothing here rewrites it.
	var denial *policy.Denial
	if errors.As(err, &denial) {
		e := Errorf(codeForDenial(denial.Kind), "%s", denial.Message)
		for k, v := range denial.Details() {
			e.WithDetail(k, v)
		}
		e.WithRequestID(requestID(ctx))
		return e
	}

	var apiErr *Error
	if errors.As(err, &apiErr) {
		if apiErr.Err.RequestID == "" {
			apiErr.WithRequestID(requestID(ctx))
		}
		return apiErr
	}

	// The diagram errors carry their own structured detail: which revisions
	// landed, who holds the lease, when it lapses. Flattening them into a
	// message would leave the client to parse prose to find out what to do.
	var stale *diagramsvc.StaleBaseError
	if errors.As(err, &stale) {
		return Errorf(CodeStaleBaseRevision, "%s", stale.Error()).
			WithDetail("base_revision", stale.Base).
			WithDetail("current_revision", stale.Current).
			WithDetail("revisions_since", revisionSummaries(stale.Since)).
			WithRequestID(requestID(ctx))
	}

	var held *diagramsvc.LeaseHeldError
	if errors.As(err, &held) {
		return Errorf(CodeLeaseHeld, "%s", held.Error()).
			WithDetail("holder_name", held.HolderName).
			WithDetail("holder_kind", held.HolderKind).
			WithDetail("expires_at", held.ExpiresAt).
			WithRequestID(requestID(ctx))
	}

	var lost *diagramsvc.LeaseLostError
	if errors.As(err, &lost) {
		// Conflict rather than lease_held: nobody else is holding it, the
		// caller's own session simply went away. The diff of what landed while
		// it was gone is what makes the message actionable.
		return Errorf(CodeConflict, "%s", lost.Error()).
			WithDetail("expired_at", lost.ExpiredAt).
			WithDetail("revisions_since", revisionSummaries(lost.Since)).
			WithRequestID(requestID(ctx))
	}

	// A closed-vocabulary violation. The message lists every valid value with
	// its meaning, because it is read by the vision model on ingest and by the
	// planner when a tool call is rejected, and a bare refusal produces a
	// second invalid value.
	var vocab *diagram.VocabularyError
	if errors.As(err, &vocab) {
		return Errorf(CodeValidationFailed, "%s", vocab.Error()).
			WithDetail("field", vocab.Field).
			WithDetail("value", vocab.Value).
			WithDetail("valid_values", vocab.Valid).
			WithRequestID(requestID(ctx))
	}

	var graphInvalid *diagram.ValidationError
	if errors.As(err, &graphInvalid) {
		return Errorf(CodeValidationFailed, "%s", graphInvalid.Message).WithRequestID(requestID(ctx))
	}

	// An accept that would put an invented name on the canvas. The detail
	// carries the slugs, so a UI can jump straight to the boxes rather than
	// making somebody parse the list out of the message.
	var illegible *ingest.IllegibleError
	if errors.As(err, &illegible) {
		return Errorf(CodeValidationFailed, "%s", illegible.Error()).
			WithDetail("unreadable", illegible.Slugs()).
			WithRequestID(requestID(ctx))
	}

	var transition *ingest.TransitionError
	if errors.As(err, &transition) {
		return Errorf(CodeConflict, "%s", capitalise(transition.Error())+".").
			WithDetail("status", string(transition.From)).
			WithRequestID(requestID(ctx))
	}

	switch {
	case errors.Is(err, domain.ErrNotFound):
		return Errorf(CodeNotFound, "%s", capitalise(strip(err, domain.ErrNotFound))+" was not found.").
			WithRequestID(requestID(ctx))

	case errors.Is(err, domain.ErrConflict):
		return Errorf(CodeConflict, "%s", capitalise(strip(err, domain.ErrConflict))+".").
			WithRequestID(requestID(ctx))

	case errors.Is(err, domain.ErrInvalid):
		return Errorf(CodeValidationFailed, "%s", capitalise(strip(err, domain.ErrInvalid))+".").
			WithRequestID(requestID(ctx))

	case errors.Is(err, domain.ErrCredentials):
		// Deliberately identical for an unknown address and a wrong password.
		// Two different messages here is an account enumeration endpoint.
		return Errorf(CodeUnauthenticated, "That email and password do not match an account.").
			WithRequestID(requestID(ctx))

	case errors.Is(err, domain.ErrInactive):
		return Errorf(CodeUnauthenticated, "That account is not active. Ask an admin to re-enable it.").
			WithRequestID(requestID(ctx))

	case errors.Is(err, domain.ErrNoSession), errors.Is(err, domain.ErrSessionExpiry):
		return Errorf(CodeUnauthenticated, "You are not signed in. Sign in again to continue.").
			WithRequestID(requestID(ctx))

	// Model failures reach the API surface from Phase 3 onward. Wired here now
	// so the mapping has one home rather than two.
	case errors.Is(err, llm.ErrContextOverflow):
		return Errorf(CodeContextOverflow, "The request was too large for the configured model context window.").
			WithCause(err).WithRequestID(requestID(ctx))

	case errors.Is(err, llm.ErrModelUnavailable), errors.Is(err, llm.ErrRateLimit):
		return Errorf(CodeModelUnavailable, "The model is not available right now. The failure is recorded against this request id.").
			WithCause(err).WithRequestID(requestID(ctx))
	}

	// Unmapped. The cause is logged in full and the client learns only the
	// request id, which is enough to find this line.
	log.ErrorContext(ctx, "unhandled error", "error", err)
	return Internal(ctx, err)
}

func codeForDenial(kind policy.DenialKind) Code {
	switch kind {
	case policy.DenialFieldNotOwned:
		return CodeFieldNotOwned
	case policy.DenialForbidden:
		return CodeForbidden
	default:
		return CodeValidationFailed
	}
}

// strip removes the sentinel prefix so the message reads as a sentence rather
// than as "not found: project".
func strip(err error, sentinel error) string {
	msg := err.Error()
	prefix := sentinel.Error() + ": "
	if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
		return msg[len(prefix):]
	}
	return msg
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}
