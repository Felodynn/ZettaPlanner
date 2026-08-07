package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer() *Server {
	return New(Deps{Log: slog.New(slog.DiscardHandler)})
}

// TestErrorEnvelopeShape covers CONTRACT §7.3: one error shape, always —
// including for the errors huma raises on its own, which is where a second
// shape would otherwise leak out.
func TestErrorEnvelopeShape(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/nothing-here", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("Content-Type = %q; the contract's envelope is served as JSON, not problem+json", ct)
	}

	var body struct {
		Error struct {
			Code      string         `json:"code"`
			Message   string         `json:"message"`
			Details   map[string]any `json:"details"`
			RequestID string         `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not the contract envelope: %v\n%s", err, rec.Body.String())
	}

	if body.Error.Code != string(CodeNotFound) {
		t.Errorf("code = %q, want %q", body.Error.Code, CodeNotFound)
	}
	if body.Error.Message == "" {
		t.Error("message is empty")
	}
	if body.Error.RequestID == "" {
		t.Error("request_id is empty; it is generated in middleware and must reach the error body")
	}
	if got := rec.Header().Get("X-Request-Id"); got != body.Error.RequestID {
		t.Errorf("X-Request-Id header %q does not match the body's request_id %q", got, body.Error.RequestID)
	}
}

// TestRequestIDIsNotClientSupplied guards the correlation id. A caller able to
// set it is a caller able to make fourteen edits look like one operation.
func TestRequestIDIsNotClientSupplied(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-Id", "forged-by-the-client")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got == "forged-by-the-client" {
		t.Error("the server echoed a client-supplied request id instead of generating its own")
	}
}

func TestHealthReportsSchemaState(t *testing.T) {
	srv := newTestServer()

	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// The wire body carries exactly the declared fields. huma's schema-link
	// hook would add a `$schema` property the contract never mentions, which
	// the generated client would then carry into every response type.
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, found := raw["$schema"]; found {
		t.Error("response body contains $schema; the contract defines the shape and nothing may be added to it")
	}

	var body HealthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding health: %v", err)
	}
	// No database is wired in this test, so health must say so rather than
	// claim to be fine.
	if body.Status != "degraded" {
		t.Errorf("status = %q; with no database configured health must not report ok", body.Status)
	}
	var sawDB bool
	for _, c := range body.Checks {
		if c.Name == "database" {
			sawDB = true
		}
	}
	if !sawDB {
		t.Error("health does not report on the database")
	}
}

// TestOpenAPIDocumentIsWellFormed is the Phase 0 exit criterion "OpenAPI
// document emits and validates". It checks the properties that actually break a
// generated client: a dangling $ref, a missing operationId, an operation with
// no declared responses.
func TestOpenAPIDocumentIsWellFormed(t *testing.T) {
	srv := newTestServer()

	raw, err := srv.OpenAPI().MarshalJSON()
	if err != nil {
		t.Fatalf("emitting: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the emitted document is not JSON: %v", err)
	}

	if v, _ := doc["openapi"].(string); !strings.HasPrefix(v, "3.1") {
		t.Errorf("openapi = %q, want 3.1.x (CONTRACT §7)", v)
	}

	paths, ok := doc["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("the document declares no paths")
	}

	seenOperationIDs := map[string]string{}
	for path, item := range paths {
		ops, _ := item.(map[string]any)
		for method, opAny := range ops {
			op, ok := opAny.(map[string]any)
			if !ok {
				continue
			}
			id, _ := op["operationId"].(string)
			if id == "" {
				t.Errorf("%s %s has no operationId; the generated client needs one to name the method", method, path)
				continue
			}
			if prev, dup := seenOperationIDs[id]; dup {
				t.Errorf("operationId %q is used by both %s and %s %s", id, prev, method, path)
			}
			seenOperationIDs[id] = method + " " + path

			responses, _ := op["responses"].(map[string]any)
			if len(responses) == 0 {
				t.Errorf("%s %s declares no responses", method, path)
			}
		}
	}

	schemas := map[string]bool{}
	if comps, ok := doc["components"].(map[string]any); ok {
		if s, ok := comps["schemas"].(map[string]any); ok {
			for name := range s {
				schemas[name] = true
			}
		}
	}
	for _, ref := range collectRefs(doc) {
		name, ok := strings.CutPrefix(ref, "#/components/schemas/")
		if !ok {
			t.Errorf("unexpected $ref form %q", ref)
			continue
		}
		if !schemas[name] {
			t.Errorf("$ref %q does not resolve; a generated client would fail on it", ref)
		}
	}

	// The error envelope must be described by the document, not only produced
	// at runtime. A document that advertises a different error shape from the
	// one the server sends is the generated lie D2 exists to prevent.
	if !schemas["ErrorBody"] {
		t.Error("the document does not describe ErrorBody; the contract's error envelope must be in the schema")
	}
	if schemas["ErrorModel"] {
		t.Error("huma's RFC 9457 ErrorModel is still in the document; the contract specifies a different shape")
	}
}

func collectRefs(v any) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if k == "$ref" {
				if s, ok := child.(string); ok {
					out = append(out, s)
				}
				continue
			}
			out = append(out, collectRefs(child)...)
		}
	case []any:
		for _, child := range t {
			out = append(out, collectRefs(child)...)
		}
	}
	return out
}

func TestCodeStatusMapping(t *testing.T) {
	cases := map[Code]int{
		CodeUnauthenticated:   http.StatusUnauthorized,
		CodeFieldNotOwned:     http.StatusForbidden,
		CodeNotFound:          http.StatusNotFound,
		CodeLeaseHeld:         http.StatusConflict,
		CodeStaleBaseRevision: http.StatusConflict,
		CodeTurnInProgress:    http.StatusConflict,
		CodeValidationFailed:  http.StatusBadRequest,
		CodeModelUnavailable:  http.StatusServiceUnavailable,
		CodeInternal:          http.StatusInternalServerError,
	}
	for code, want := range cases {
		if got := code.Status(); got != want {
			t.Errorf("%q maps to %d, want %d", code, got, want)
		}
	}
}

// TestDenialNamesRoleAndRoute is CONTRACT §7.3: a denial's wording is a prompt
// layer, and every 403 names the owning role and the available route.
func TestDenialNamesRoleAndRoute(t *testing.T) {
	err := FieldNotOwned("due_date", "manager", "developer", "raise a proposal")

	msg := err.Message()
	for _, want := range []string{"due_date", "manager", "developer", "raise a proposal"} {
		if !strings.Contains(msg, want) {
			t.Errorf("denial text is missing %q: %s", want, msg)
		}
	}
	// Lead with the action: a refusal that leads with the reason gets prose
	// back, and one that leads with the action gets the action.
	if !strings.HasPrefix(msg, "To change due_date, raise a proposal") {
		t.Errorf("denial does not lead with the route forward: %s", msg)
	}
	if err.GetStatus() != http.StatusForbidden {
		t.Errorf("status = %d, want 403", err.GetStatus())
	}
	if err.Err.Details["owning_role"] != "manager" {
		t.Errorf("details.owning_role = %v, want manager", err.Err.Details["owning_role"])
	}
}
