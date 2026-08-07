package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// envelopeMiddleware rewrites the router's own plain-text failures into the
// contract envelope.
//
// CONTRACT §7.3 says one shape, always. Handler errors already wear it, but
// http.ServeMux answers an unmatched path with `404 page not found` as
// text/plain and a wrong method with `Method Not Allowed`, both written before
// any of our code runs. A frontend that has to parse two error formats — one of
// which is prose — has a second code path that only executes when something has
// already gone wrong, which is where it will be least tested.
//
// Interception is deliberately narrow: only a 404 or 405 that the router
// declared as text/plain. Anything huma produced is already JSON and passes
// straight through.
func envelopeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ew := &envelopeWriter{ResponseWriter: w, request: r}
		next.ServeHTTP(ew, r)
		ew.finish()
	})
}

type envelopeWriter struct {
	http.ResponseWriter
	request *http.Request

	status      int
	intercept   bool
	wroteHeader bool
}

func (w *envelopeWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status

	if (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) &&
		strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		// Hold the header back: finish will write the envelope instead.
		w.intercept = true
		return
	}

	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *envelopeWriter) Write(b []byte) (int, error) {
	if w.intercept {
		// Swallow the router's prose. Reported as written so the caller sees no
		// short-write error.
		return len(b), nil
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Flush keeps Server-Sent Events working through the wrapper (CONTRACT §7.2).
func (w *envelopeWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *envelopeWriter) finish() {
	if !w.intercept {
		return
	}
	w.intercept = false

	var apiErr *Error
	switch w.status {
	case http.StatusMethodNotAllowed:
		// The contract's code set has no method-specific code, and adding one
		// would widen a closed set for a case that means "this operation does
		// not exist". The status still says 405 so the Allow header is useful.
		apiErr = Errorf(CodeNotFound, "%s is not allowed on %s.", w.request.Method, w.request.URL.Path).
			WithStatus(http.StatusMethodNotAllowed).
			WithDetail("path", w.request.URL.Path).
			WithDetail("method", w.request.Method)
		if allow := w.Header().Get("Allow"); allow != "" {
			apiErr.WithDetail("allowed_methods", strings.Split(allow, ", "))
		}
	default:
		apiErr = Errorf(CodeNotFound, "No route matches %s %s.", w.request.Method, w.request.URL.Path).
			WithDetail("path", w.request.URL.Path).
			WithDetail("method", w.request.Method)
	}
	apiErr.WithRequestID(telemetry.RequestID(w.request.Context()))

	body, err := json.Marshal(apiErr)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.wroteHeader = true
		w.ResponseWriter.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", itoa(len(body)))
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(apiErr.GetStatus())
	_, _ = w.ResponseWriter.Write(body)
}
