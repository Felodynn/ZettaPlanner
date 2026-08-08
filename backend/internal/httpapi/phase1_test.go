package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/httpapi"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/auth"
	diagramsvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/diagram"
	plansvc "github.com/Felodynn/ZettaPlanner/backend/internal/service/plan"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/project"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
	"github.com/Felodynn/ZettaPlanner/backend/internal/testdb"
)

// End-to-end over the real HTTP surface and a real Postgres. The database is
// the product; a mock of it would test nothing (BACKEND.md §1.2).
//
//	ZP_TEST_DATABASE_URL=postgres:///zettaplanner_test go test ./internal/httpapi/

const testPassword = "a-long-enough-password"

type harness struct {
	t   *testing.T
	srv *httptest.Server
	db  *store.DB
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, ctx := testdb.Fresh(t, "httpapi")
	quiet := slog.New(slog.DiscardHandler)
	if _, err := store.MigrateUp(ctx, db, quiet); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	api := httpapi.New(httpapi.Deps{
		DB:       db,
		Auth:     auth.New(db, quiet, 0),
		Projects: project.New(db, quiet),
		Diagrams: diagramsvc.New(db, quiet, 0),
		Plan:     plansvc.New(db, quiet),
		Log:      quiet,
	})
	srv := httptest.NewServer(api.Handler)
	t.Cleanup(srv.Close)

	return &harness{t: t, srv: srv, db: db}
}

// client is one logged-in caller, holding its own cookie jar.
type client struct {
	h      *harness
	cookie *http.Cookie
	userID uuid.UUID
}

func (h *harness) bootstrapAdmin() *client {
	h.t.Helper()
	svc := auth.New(h.db, slog.New(slog.DiscardHandler), 0)
	ctx := telemetry.WithRequestID(context.Background(), telemetry.NewRequestID())

	user, err := svc.Bootstrap(ctx, auth.CreateUserInput{
		Email: "admin@test.local", DisplayName: "Admin",
		Password: testPassword, Role: policy.RoleAdmin,
	})
	if err != nil {
		h.t.Fatalf("bootstrapping the first admin: %v", err)
	}
	c := &client{h: h, userID: user.ID}
	c.login("admin@test.local", testPassword)
	return c
}

// createUser makes an account through the API, as this client.
func (c *client) createUser(email, name string, role policy.Role) *client {
	c.h.t.Helper()
	var out struct {
		ID uuid.UUID `json:"id"`
	}
	resp := c.do(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": email, "display_name": name, "password": testPassword, "role": string(role),
	})
	c.h.decode(resp, http.StatusCreated, &out)

	other := &client{h: c.h, userID: out.ID}
	other.login(email, testPassword)
	return other
}

func (c *client) login(email, password string) {
	c.h.t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequest(http.MethodPost, c.h.srv.URL+"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.h.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		c.h.t.Fatalf("login as %s: HTTP %d", email, resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == httpapi.SessionCookie {
			c.cookie = ck
		}
	}
	if c.cookie == nil {
		c.h.t.Fatal("login returned no session cookie")
	}
}

func (c *client) do(method, path string, body any) *http.Response {
	c.h.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, c.h.srv.URL+path, reader)
	if err != nil {
		c.h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.h.t.Fatal(err)
	}
	return resp
}

func (h *harness) decode(resp *http.Response, wantStatus int, into any) {
	h.t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		h.t.Fatalf("HTTP %d, want %d: %s", resp.StatusCode, wantStatus, buf.String())
	}
	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			h.t.Fatalf("decoding: %v", err)
		}
	}
}

type errBody struct {
	Error struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Details   map[string]any `json:"details"`
		RequestID string         `json:"request_id"`
	} `json:"error"`
}

func (h *harness) decodeErr(resp *http.Response, wantStatus int) errBody {
	h.t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		h.t.Fatalf("HTTP %d, want %d: %s", resp.StatusCode, wantStatus, buf.String())
	}
	var out errBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		h.t.Fatalf("decoding the error envelope: %v", err)
	}
	return out
}

func (h *harness) createProject(c *client, key, name string) uuid.UUID {
	h.t.Helper()
	var out struct {
		ID uuid.UUID `json:"id"`
	}
	h.decode(c.do(http.MethodPost, "/api/v1/projects", map[string]any{
		"key": key, "name": name,
	}), http.StatusCreated, &out)
	return out.ID
}

// ── the exit criteria ────────────────────────────────────────────────────────

// TestFieldOwnershipDenialNamesRoleAndRoute is the Phase 1 manual test, as a
// test: a developer patching a manager-owned field gets 403 field_not_owned
// naming the manager and naming the route forward.
func TestFieldOwnershipDenialNamesRoleAndRoute(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()
	dev := admin.createUser("dev@test.local", "Dev", policy.RoleDeveloper)

	pid := h.createProject(admin, "ZP", "ZettaPlanner")
	h.decode(admin.do(http.MethodPut,
		"/api/v1/projects/"+pid.String()+"/members/"+dev.userID.String(),
		map[string]any{"role": "developer"}), http.StatusNoContent, nil)

	body := h.decodeErr(dev.do(http.MethodPatch, "/api/v1/projects/"+pid.String(),
		map[string]any{"name": "Renamed by a developer"}), http.StatusForbidden)

	if body.Error.Code != "field_not_owned" {
		t.Errorf("code = %q, want field_not_owned", body.Error.Code)
	}
	// Lead with the action: a refusal that leads with the reason gets prose
	// back, one that leads with the action gets the action (§7.3).
	if !strings.HasPrefix(body.Error.Message, "To change name, raise a proposal") {
		t.Errorf("the denial does not lead with the route: %q", body.Error.Message)
	}
	if body.Error.Details["owning_role"] != "manager" {
		t.Errorf("details.owning_role = %v, want manager", body.Error.Details["owning_role"])
	}
	if body.Error.Details["effective_role"] != "developer" {
		t.Errorf("details.effective_role = %v, want developer", body.Error.Details["effective_role"])
	}
	if body.Error.RequestID == "" {
		t.Error("the denial carries no request id")
	}
}

// TestProjectOverrideBeatsGlobalRole is the criterion that a per-project role
// override wins — including downward, which is the direction that would
// otherwise be a permission nobody can take away.
func TestProjectOverrideBeatsGlobalRole(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()
	mgr := admin.createUser("mgr@test.local", "Manager", policy.RoleManager)

	pid := h.createProject(admin, "ZP", "ZettaPlanner")
	memberPath := "/api/v1/projects/" + pid.String() + "/members/" + mgr.userID.String()

	// A global manager, added to this project as a developer.
	h.decode(admin.do(http.MethodPut, memberPath, map[string]any{"role": "developer"}),
		http.StatusNoContent, nil)

	body := h.decodeErr(mgr.do(http.MethodPatch, "/api/v1/projects/"+pid.String(),
		map[string]any{"name": "Renamed"}), http.StatusForbidden)
	if body.Error.Details["effective_role"] != "developer" {
		t.Fatalf("a global manager overridden to developer has effective role %v; the override must win",
			body.Error.Details["effective_role"])
	}

	// Promote them on this project, and the same request succeeds.
	h.decode(admin.do(http.MethodPut, memberPath, map[string]any{"role": "manager"}),
		http.StatusNoContent, nil)
	h.decode(mgr.do(http.MethodPatch, "/api/v1/projects/"+pid.String(),
		map[string]any{"name": "Renamed"}), http.StatusOK, nil)
}

// TestExactlyOneAuditRowPerMutatingRequest is the criterion in its strongest
// form: not "at least one", not "one per row touched", but exactly one per
// logical operation, sharing the request id.
func TestExactlyOneAuditRowPerMutatingRequest(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()
	dev := admin.createUser("dev@test.local", "Dev", policy.RoleDeveloper)

	// Project creation touches two tables — the project and the creator's
	// membership — and must still be one entry.
	pid := h.createProject(admin, "ZP", "ZettaPlanner")
	h.decode(admin.do(http.MethodPut,
		"/api/v1/projects/"+pid.String()+"/members/"+dev.userID.String(),
		map[string]any{"role": "developer"}), http.StatusNoContent, nil)
	h.decode(admin.do(http.MethodPatch, "/api/v1/projects/"+pid.String(),
		map[string]any{"description": "dogfood"}), http.StatusOK, nil)

	ctx := context.Background()
	rows, err := h.db.Pool.Query(ctx,
		`SELECT request_id, count(*) FROM audit_log GROUP BY request_id HAVING count(*) <> 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			t.Fatal(err)
		}
		t.Errorf("request %s wrote %d audit rows; one logical operation is one entry", id, n)
	}

	// And every mutating request wrote one. Four so far: the bootstrap, the
	// dev account, the project, the membership, the patch — plus a session row
	// per login.
	var writes int
	if err := h.db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action IN
		   ('project.created','project.updated','project_member.added','user.created')`).Scan(&writes); err != nil {
		t.Fatal(err)
	}
	if writes != 5 {
		t.Errorf("recorded %d mutating operations, want 5 (bootstrap admin, dev account, project, membership, patch)", writes)
	}
}

// TestLoginRotatesTheSession: the identifier a request arrived with does not
// survive a login, so a fixed session cannot be promoted to an authenticated
// one.
func TestLoginRotatesTheSession(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()

	first := admin.cookie.Value
	admin.login("admin@test.local", testPassword)
	second := admin.cookie.Value

	if first == second {
		t.Fatal("the session token did not change across a login")
	}

	// The old token must no longer authenticate.
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/auth/me", nil)
	req.AddCookie(&http.Cookie{Name: httpapi.SessionCookie, Value: first})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the pre-login session still works: HTTP %d", resp.StatusCode)
	}
}

func TestLogoutRevokes(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()

	h.decode(admin.do(http.MethodPost, "/api/v1/auth/logout", nil), http.StatusNoContent, nil)
	h.decodeErr(admin.do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusUnauthorized)
}

// TestDeactivationRevokesSessions: a live session must not outlast the
// authority it was issued under.
func TestDeactivationRevokesSessions(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()
	dev := admin.createUser("dev@test.local", "Dev", policy.RoleDeveloper)

	h.decode(dev.do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusOK, nil)

	h.decode(admin.do(http.MethodPatch, "/api/v1/admin/users/"+dev.userID.String(),
		map[string]any{"is_active": false}), http.StatusOK, nil)

	h.decodeErr(dev.do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusUnauthorized)
}

// TestRoleChangeRevokesSessions: the same reasoning as deactivation. A session
// issued to a manager must not keep manager authority after a demotion.
func TestRoleChangeRevokesSessions(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()
	mgr := admin.createUser("mgr@test.local", "Manager", policy.RoleManager)

	h.decode(admin.do(http.MethodPatch, "/api/v1/admin/users/"+mgr.userID.String(),
		map[string]any{"role": "developer"}), http.StatusOK, nil)

	h.decodeErr(mgr.do(http.MethodGet, "/api/v1/auth/me", nil), http.StatusUnauthorized)
}

// TestNonMemberGetsNotFound: distinguishing "you may not see this" from "this
// does not exist" would let anyone enumerate the project list.
func TestNonMemberGetsNotFound(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()
	outsider := admin.createUser("outsider@test.local", "Outsider", policy.RoleDeveloper)

	pid := h.createProject(admin, "ZP", "ZettaPlanner")

	body := h.decodeErr(outsider.do(http.MethodGet, "/api/v1/projects/"+pid.String(), nil),
		http.StatusNotFound)
	if body.Error.Code != "not_found" {
		t.Errorf("code = %q, want not_found", body.Error.Code)
	}

	var out struct {
		Projects []any `json:"projects"`
	}
	h.decode(outsider.do(http.MethodGet, "/api/v1/projects", nil), http.StatusOK, &out)
	if len(out.Projects) != 0 {
		t.Errorf("a non-member sees %d projects, want 0", len(out.Projects))
	}
}

// TestNonAdminCannotManageUsersOrMembership pins the coarse admin checks.
func TestNonAdminCannotManageUsersOrMembership(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()
	mgr := admin.createUser("mgr@test.local", "Manager", policy.RoleManager)
	pid := h.createProject(admin, "ZP", "ZettaPlanner")

	h.decodeErr(mgr.do(http.MethodGet, "/api/v1/admin/users", nil), http.StatusForbidden)
	h.decodeErr(mgr.do(http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "x@test.local", "display_name": "X", "password": testPassword, "role": "developer",
	}), http.StatusForbidden)

	body := h.decodeErr(mgr.do(http.MethodPut,
		"/api/v1/projects/"+pid.String()+"/members/"+mgr.userID.String(),
		map[string]any{"role": "manager"}), http.StatusForbidden)
	if !strings.Contains(body.Error.Message, "ask an admin") {
		t.Errorf("the denial should name the route: %q", body.Error.Message)
	}
}

// TestCredentialFailuresAreIndistinguishable: two different messages here is an
// account enumeration endpoint.
func TestCredentialFailuresAreIndistinguishable(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()

	wrongPassword := h.decodeErr(admin.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "admin@test.local", "password": "not-the-password"}),
		http.StatusUnauthorized)
	unknownEmail := h.decodeErr(admin.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "nobody@test.local", "password": "not-the-password"}),
		http.StatusUnauthorized)

	if wrongPassword.Error.Message != unknownEmail.Error.Message {
		t.Errorf("a wrong password and an unknown address answer differently:\n  %q\n  %q",
			wrongPassword.Error.Message, unknownEmail.Error.Message)
	}
	if wrongPassword.Error.Code != "unauthenticated" {
		t.Errorf("code = %q, want unauthenticated", wrongPassword.Error.Code)
	}
}

// TestSessionCookieFlags: HttpOnly so no script can read it, SameSite=Lax so a
// cross-site post cannot carry it.
func TestSessionCookieFlags(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()

	if !admin.cookie.HttpOnly {
		t.Error("the session cookie is not HttpOnly")
	}
	if admin.cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", admin.cookie.SameSite)
	}
	if admin.cookie.Path != "/" {
		t.Errorf("Path = %q, want /", admin.cookie.Path)
	}
}

// TestSessionTokenIsNotStored: the database holds a hash, so a dump does not
// hand over live sessions.
func TestSessionTokenIsNotStored(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()

	var n int
	err := h.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sessions WHERE encode(token_hash, 'escape') = $1`, admin.cookie.Value).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the raw session token appears in the sessions table")
	}

	var stored int
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM sessions WHERE octet_length(token_hash) = 32`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == 0 {
		t.Error("no session row holds a 32-byte SHA-256")
	}
}

// TestUserDeletionIsSoft: every audit entry naming this account must stay
// joinable (§6.5 constraint 1).
func TestUserDeletionIsSoft(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()
	dev := admin.createUser("dev@test.local", "Dev", policy.RoleDeveloper)

	h.decode(admin.do(http.MethodDelete, "/api/v1/admin/users/"+dev.userID.String(), nil),
		http.StatusNoContent, nil)

	var deletedAt *string
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT deleted_at::text FROM users WHERE id = $1`, dev.userID).Scan(&deletedAt); err != nil {
		t.Fatalf("the row is gone, not soft-deleted: %v", err)
	}
	if deletedAt == nil {
		t.Error("deleted_at was not set")
	}

	// The audit entries naming this account still join to it.
	var joinable int
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log a JOIN actors ac ON ac.id = a.actor_id
		  WHERE a.subject_id = $1 OR ac.user_id = $1`, dev.userID).Scan(&joinable); err != nil {
		t.Fatal(err)
	}
	if joinable == 0 {
		t.Error("no audit entry survives that names the deleted account")
	}
}

// TestPasswordIsNeverInTheAuditLog: the log is never pruned, so a secret
// written into it once is there permanently.
func TestPasswordIsNeverInTheAuditLog(t *testing.T) {
	h := newHarness(t)
	admin := h.bootstrapAdmin()
	admin.createUser("dev@test.local", "Dev", policy.RoleDeveloper)

	var hits int
	err := h.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log
		  WHERE before::text ILIKE '%argon2%' OR after::text ILIKE '%argon2%'
		     OR before::text ILIKE '%password%' OR after::text ILIKE '%password%'`).Scan(&hits)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 0 {
		t.Errorf("%d audit rows mention a password or a hash", hits)
	}
}
