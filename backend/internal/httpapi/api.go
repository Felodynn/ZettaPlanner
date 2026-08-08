// Package httpapi is the HTTP boundary: middleware, handlers, the single error
// mapping, and the OpenAPI document.
//
// OpenAPI 3.1 is emitted by the backend and is authoritative for request and
// response shapes (CONTRACT §7). The frontend's TypeScript client is generated
// from it and never hand-written — a generated JS client is a generated lie
// (D2), and a hand-written TypeScript one drifts.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/Felodynn/ZettaPlanner/backend/internal/llm"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/auth"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/contextsvc"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/export"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/governance"
	ingestsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/ingest"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/project"
	threadsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/thread"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// Version is stamped into the OpenAPI document and the health response.
const Version = "0.0.1"

func init() {
	// Every error leaving this process wears the contract's envelope, including
	// the ones huma raises itself for request validation and routing. Mapping
	// them in one place is the whole point: a second error shape is a second
	// thing the generated client has to know about.
	//
	// Both hooks are overridden. NewErrorWithContext is what runs per request
	// and is the only one that can reach the request id; NewError is what huma
	// calls once at registration time to derive the error schema for the
	// OpenAPI document, so overriding it is what stops the document describing
	// a shape this server never sends.
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return newAPIError("", status, msg, errs...)
	}
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		var path string
		if ctx != nil {
			// The path disambiguates which closed vocabulary a field belongs
			// to. `body.status` is a work item's set on one route and a
			// milestone's deliberately coarser set on another, and guessing
			// from the value would teach the wrong one exactly when somebody
			// sent a value that is valid in the other.
			url := ctx.URL()
			path = url.Path
		}
		err := newAPIError(path, status, msg, errs...)
		if ctx != nil {
			err.WithRequestID(telemetry.RequestID(ctx.Context()))
		}
		return err
	}
}

// newAPIError normalises whatever huma raised into the contract envelope. An
// *Error already in the chain wins: a handler that took the trouble to write
// teaching denial text must not have it replaced by a status code's default.
func newAPIError(path string, status int, msg string, errs ...error) *Error {
	var apiErr *Error
	for _, err := range errs {
		if errors.As(err, &apiErr) {
			break
		}
	}
	if apiErr == nil {
		// The status comes from the code, not from whatever huma raised.
		// huma answers a body-schema violation with 422 while our own
		// validation_failed is 400, and one contract code mapping to two
		// statuses is a thing the generated client would have to special-case.
		apiErr = Errorf(codeForStatus(status), "%s", msg)
	}
	if details := errorDetails(errs); len(details) > 0 {
		apiErr.WithDetail("errors", details)
		// A schema failure on a closed-set field is replaced by the domain's
		// teaching error, so the message says what the valid values *mean*
		// rather than only that the request failed. See vocabulary.go.
		if lesson := teachClosedVocabulary(path, details); lesson != "" {
			apiErr.Err.Message = lesson
		}
	}
	return apiErr
}

func codeForStatus(status int) Code {
	switch status {
	case http.StatusUnauthorized:
		return CodeUnauthenticated
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return CodeValidationFailed
	case http.StatusServiceUnavailable:
		return CodeModelUnavailable
	default:
		return CodeInternal
	}
}

func errorDetails(errs []error) []map[string]any {
	var out []map[string]any
	for _, err := range errs {
		if err == nil {
			continue
		}
		var detailer huma.ErrorDetailer
		if errors.As(err, &detailer) {
			d := detailer.ErrorDetail()
			out = append(out, map[string]any{
				"message":  d.Message,
				"location": d.Location,
				"value":    d.Value,
			})
			continue
		}
		out = append(out, map[string]any{"message": err.Error()})
	}
	return out
}

// Deps are the collaborators the API needs. All of them may be nil when the
// only goal is to emit the OpenAPI document, which is why `zettaplanner
// openapi` needs neither a database nor a model server.
type Deps struct {
	DB         *store.DB
	Registry   *llm.Registry
	Auth       *auth.Service
	Projects   *project.Service
	Diagrams   *diagramsvc.Service
	Ingests    *ingestsvc.Service
	Plan       *plansvc.Service
	Context    *contextsvc.Service
	Threads    *threadsvc.Service
	Governance *governance.Service
	Export     *export.Service

	// Events is the SSE fan-out. Constructed by the caller because the agent
	// runner and the thread service publish into it and are built before the
	// server is. Nil means a hub is made here and nothing else can publish.
	Events  *EventHub
	Log     *slog.Logger
	Started time.Time

	// SecureCookies marks the session cookie Secure. Configuration rather than
	// a constant because development runs on plain http, and a cookie the
	// browser refuses to store is a login that silently does nothing.
	SecureCookies bool

	// TrustProxy makes X-Forwarded-For authoritative for the client address
	// written to the audit log. Off unless the deployment is actually behind a
	// proxy: trusting it otherwise lets any caller choose what gets recorded.
	TrustProxy bool

	// DevOrigin is a single browser origin allowed to call this API with
	// credentials. Empty in every deployment — see devCORSMiddleware.
	DevOrigin string
}

// Server is a built API: an http.Handler plus the huma API the document is
// read from.
type Server struct {
	Handler http.Handler
	API     huma.API
	deps    Deps

	// events is the per-project SSE fan-out (§7.2). Always constructed, so a
	// caller may publish into it before deciding whether to serve the stream.
	events *EventHub
	// shutdown closes every live stream when the process is going away, rather
	// than leaving clients waiting on a socket nothing will ever write to.
	shutdown  chan struct{}
	closeOnce sync.Once
}

// Events is the hub the services publish into.
func (s *Server) Events() *EventHub { return s.events }

// Close ends every live event stream. Idempotent.
func (s *Server) Close() { s.closeOnce.Do(func() { close(s.shutdown) }) }

// New builds the router, registers every operation, and returns the server.
func New(deps Deps) *Server {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.Started.IsZero() {
		deps.Started = time.Now()
	}

	mux := http.NewServeMux()

	cfg := huma.DefaultConfig("ZettaPlanner", Version)
	cfg.Info.Description = "AI-native technical planning substrate. " +
		"This document is emitted by the backend and is authoritative for request and response shapes."
	cfg.DocsPath = "/docs"
	// huma appends the extension: the document is served at /openapi.json and
	// /openapi.yaml.
	cfg.OpenAPIPath = "/openapi"
	cfg.Servers = []*huma.Server{{URL: "/", Description: "This deployment"}}

	// Drop huma's schema-link hook. It injects a `$schema` property into every
	// response body and a Link header alongside it. The contract defines the
	// response shapes and the TypeScript client is generated from them; a field
	// present in the wire body that the contract never mentions is drift, and it
	// leaks the deployment's own hostname into every payload.
	cfg.CreateHooks = nil
	cfg.SchemasPath = ""

	api := humago.New(mux, cfg)
	if deps.Events == nil {
		deps.Events = NewEventHub()
	}
	s := &Server{API: api, deps: deps, events: deps.Events, shutdown: make(chan struct{})}

	s.registerHealth()
	if deps.Auth != nil {
		s.registerAuth()
		s.registerUsers()
	}
	if deps.Projects != nil {
		s.registerProjects()
		s.registerAudit()
		s.registerOwnership()
	}
	if deps.Diagrams != nil {
		s.registerDiagrams()
	}
	if deps.Ingests != nil {
		s.registerIngests()
	}
	if deps.Plan != nil {
		s.registerPlan()
		s.registerDashboard()
	}
	if deps.Context != nil {
		s.registerDigest()
	}
	if deps.Threads != nil {
		s.registerThreads()
		// Registered on the mux directly: a long-lived text/event-stream is not
		// a request/response operation, and describing it as one in the OpenAPI
		// document would generate a client method that hangs.
		s.registerEvents(mux)
	}
	if deps.Governance != nil {
		s.registerProposals()
	}
	if deps.Export != nil {
		s.registerExports()
	}

	// Middleware wraps the mux rather than the huma API so that it also covers
	// the docs and openapi routes, and so the request id exists before huma
	// begins validating anything.
	//
	// Order matters: the request id is generated outermost so every log line and
	// every audit row inside can reach it, and the session is resolved innermost
	// so that only routed requests pay for the lookup.
	handler := s.sessionMiddleware(mux)
	handler = envelopeMiddleware(handler)
	handler = clientIPMiddleware(deps.TrustProxy, handler)
	handler = loggingMiddleware(deps.Log, handler)
	handler = devCORSMiddleware(deps.DevOrigin, handler)
	s.Handler = requestIDMiddleware(handler)
	return s
}

// devCORSMiddleware allows exactly one origin to call this API with cookies.
//
// **There is no CORS in a deployment.** The SPA is embedded in this binary
// (D2), so the browser and the API share an origin and the question never
// arises. This exists for the frontend dev server, which runs on its own port,
// and it is off unless ZP_DEV_ORIGIN names a specific origin.
//
// One origin, echoed back exactly — never `*`, which browsers refuse to combine
// with credentials anyway, and never a reflected Origin header, which is the
// same thing as no policy at all. The `Vary` is not decoration: without it a
// cache in front of this process can serve one origin's preflight answer to
// another.
func devCORSMiddleware(origin string, next http.Handler) http.Handler {
	if origin == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if r.Header.Get("Origin") == origin {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", "X-Request-Id, Content-Disposition")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type, Last-Event-ID")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RegisterOperations exposes the registered paths, for tests that assert the
// surface matches CONTRACT §7.1.
func (s *Server) RegisterOperations() []string {
	var out []string
	for path, item := range s.API.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodGet:    item.Get,
			http.MethodPost:   item.Post,
			http.MethodPut:    item.Put,
			http.MethodPatch:  item.Patch,
			http.MethodDelete: item.Delete,
		} {
			if op != nil {
				out = append(out, method+" "+path)
			}
		}
	}
	sort.Strings(out)
	return out
}

// OpenAPI returns the generated document.
func (s *Server) OpenAPI() *huma.OpenAPI { return s.API.OpenAPI() }

// ── request id ───────────────────────────────────────────────────────────────

// requestIDMiddleware generates the correlation id for the request.
//
// Always generated here, never taken from the client: it is written to
// audit_log as the thing that groups one logical operation, and a caller able
// to supply it is a caller able to make fourteen edits look like one.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := telemetry.NewRequestID()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(telemetry.WithRequestID(r.Context(), id)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Flush lets Server-Sent Events through (CONTRACT §7.2).
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func loggingMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		log.InfoContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// ── health ───────────────────────────────────────────────────────────────────

// HealthBody is the health response.
type HealthBody struct {
	Status        string        `json:"status" doc:"ok when every check passed, degraded otherwise" enum:"ok,degraded"`
	Version       string        `json:"version"`
	UptimeSeconds int64         `json:"uptime_seconds"`
	SchemaVersion int           `json:"schema_version" doc:"Highest applied migration; 0 means the database has not been migrated"`
	Checks        []HealthCheck `json:"checks"`
}

// HealthCheck is one dependency's state.
type HealthCheck struct {
	Name   string `json:"name"`
	Status string `json:"status" enum:"ok,failing,skipped"`
	Detail string `json:"detail,omitempty"`
}

type healthOutput struct {
	Body HealthBody
}

func (s *Server) registerHealth() {
	huma.Register(s.API, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Liveness and dependency status",
		Description: "Reports the schema version and the state of each dependency. " +
			"Model validation is not re-run here; it happens at startup and fails the process.",
		Tags: []string{"ops"},
	}, func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		out := &healthOutput{Body: HealthBody{
			Status:        "ok",
			Version:       Version,
			UptimeSeconds: int64(time.Since(s.deps.Started).Seconds()),
		}}

		if s.deps.DB == nil {
			out.Body.Checks = append(out.Body.Checks, HealthCheck{
				Name: "database", Status: "skipped", Detail: "no pool configured",
			})
			out.Body.Status = "degraded"
		} else {
			pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := s.deps.DB.Pool.Ping(pingCtx); err != nil {
				out.Body.Checks = append(out.Body.Checks, HealthCheck{
					Name: "database", Status: "failing", Detail: err.Error(),
				})
				out.Body.Status = "degraded"
			} else {
				out.Body.Checks = append(out.Body.Checks, HealthCheck{Name: "database", Status: "ok"})
				if v, err := store.SchemaVersion(pingCtx, s.deps.DB.Pool); err == nil {
					out.Body.SchemaVersion = v
				}
			}
		}

		if s.deps.Registry == nil {
			out.Body.Checks = append(out.Body.Checks, HealthCheck{
				Name: "models", Status: "skipped", Detail: "no registry configured",
			})
		} else {
			for _, rep := range s.deps.Registry.Reports() {
				check := HealthCheck{Name: "model:" + rep.Name, Status: "ok"}
				if rep.Failed() {
					check.Status = "failing"
					check.Detail = rep.Problems[0]
					out.Body.Status = "degraded"
				} else {
					check.Detail = describeProof(rep)
				}
				out.Body.Checks = append(out.Body.Checks, check)
			}
		}
		return out, nil
	})
}

func describeProof(rep *llm.Report) string {
	switch rep.ProofMethod {
	case llm.ProofPS:
		return "allocated window " + itoa(rep.ProvenCtx) + " confirmed by /api/ps"
	case llm.ProofProbe:
		return "window of at least " + itoa(rep.ProvenCtx) + " proven by context probe"
	default:
		return ""
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
