package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/telemetry"
)

// SessionCookie is the cookie name.
const SessionCookie = "zp_session"

type sessionCtxKey int

const sessionIDKey sessionCtxKey = iota

// sessionMiddleware resolves the session cookie to a principal and puts both in
// the request context.
//
// It never rejects. An absent or unusable cookie simply means no principal, and
// the handlers that require one say so themselves — a middleware that 401s
// every unauthenticated request would also 401 the login endpoint.
func (s *Server) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Auth == nil {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(SessionCookie)
		if err != nil || cookie.Value == "" {
			next.ServeHTTP(w, r)
			return
		}

		principal, sessionID, err := s.deps.Auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			// A stale cookie is cleared on the way past, so a browser holding a
			// revoked session stops presenting it on every subsequent request.
			http.SetCookie(w, s.clearCookie())
			next.ServeHTTP(w, r)
			return
		}

		ctx := policy.WithPrincipal(r.Context(), *principal)
		ctx = context.WithValue(ctx, sessionIDKey, sessionID)
		s.deps.Auth.Touch(ctx, sessionID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func sessionIDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(sessionIDKey).(uuid.UUID)
	return id, ok
}

// requirePrincipal returns the caller, or the contract's unauthenticated error.
func requirePrincipal(ctx context.Context) (policy.Principal, error) {
	p, ok := policy.PrincipalFrom(ctx)
	if !ok {
		return policy.Principal{}, Errorf(CodeUnauthenticated,
			"To do this, sign in. This request carried no valid session.").
			WithRequestID(telemetry.RequestID(ctx))
	}
	return p, nil
}

func requestID(ctx context.Context) string { return telemetry.RequestID(ctx) }

// sessionCookie builds the cookie for a freshly issued session.
//
// HttpOnly so no script can read it; SameSite=Lax so a cross-site form post
// cannot carry it; Secure whenever the deployment is served over TLS. Secure is
// configuration rather than a constant because development runs on plain http,
// and a cookie the browser refuses to store is a login that silently does
// nothing.
func (s *Server) sessionCookie(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.deps.SecureCookies,
	}
}

func (s *Server) clearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.deps.SecureCookies,
	}
}
