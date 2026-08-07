package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/auth"
)

// ── wire shapes ──────────────────────────────────────────────────────────────

// UserSummary is a user as the API presents one. No password hash, ever.
type UserSummary struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role" enum:"admin,manager,developer" doc:"Deployment-wide role, before any per-project override"`
	IsActive    bool      `json:"is_active"`
}

// ProjectRoleSummary is the caller's resolved role on one project.
type ProjectRoleSummary struct {
	ProjectID   uuid.UUID `json:"project_id"`
	ProjectKey  string    `json:"project_key"`
	ProjectName string    `json:"project_name"`
	// Effective governs writes: the per-project override when there is one,
	// otherwise the global role, and always admin for an admin.
	Effective string  `json:"effective_role" enum:"admin,manager,developer"`
	Override  *string `json:"override_role,omitempty" enum:"admin,manager,developer" doc:"The per-project override, when one is set"`
}

// MeBody is the authenticated caller.
type MeBody struct {
	User     UserSummary          `json:"user"`
	Projects []ProjectRoleSummary `json:"projects"`
}

type meOutput struct {
	Body MeBody
}

type loginInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Email    string `json:"email" format:"email" doc:"The account's email address"`
		Password string `json:"password" minLength:"1" doc:"The account's password"`
	}
}

type loginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      MeBody
}

// logoutOutput carries only the clearing cookie. Deliberately no Status field:
// huma reads a field named Status as the response status code, and leaving it
// zero panics the handler. The 204 comes from the operation's DefaultStatus.
type logoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

// ── registration ─────────────────────────────────────────────────────────────

func (s *Server) registerAuth() {
	huma.Register(s.API, huma.Operation{
		OperationID: "login",
		Method:      http.MethodPost,
		Path:        "/api/v1/auth/login",
		Summary:     "Sign in",
		Description: "Verifies a password and issues a session cookie. Any session the request already " +
			"carried is revoked, so a session identifier never survives a login.",
		Tags: []string{"auth"},
	}, func(ctx context.Context, in *loginInput) (*loginOutput, error) {
		var current *uuid.UUID
		if id, ok := sessionIDFrom(ctx); ok {
			current = &id
		}

		result, err := s.deps.Auth.Login(ctx, auth.LoginInput{
			Email:            in.Body.Email,
			Password:         in.Body.Password,
			CurrentSessionID: current,
			UserAgent:        in.UserAgent,
			IP:               clientIP(ctx),
		})
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}

		me, err := s.deps.Auth.Me(ctx, result.Principal)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &loginOutput{
			SetCookie: *s.sessionCookie(result.Token, result.ExpiresAt),
			Body:      meBody(me),
		}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "logout",
		Method:        http.MethodPost,
		Path:          "/api/v1/auth/logout",
		Summary:       "Sign out",
		Description:   "Revokes the current session and clears the cookie. Revoked, not deleted: when a session ended stays answerable.",
		Tags:          []string{"auth"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*logoutOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		id, ok := sessionIDFrom(ctx)
		if ok {
			if err := s.deps.Auth.Logout(ctx, id, p); err != nil {
				return nil, s.mapErr(ctx, err)
			}
		}
		return &logoutOutput{SetCookie: *s.clearCookie()}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "me",
		Method:      http.MethodGet,
		Path:        "/api/v1/auth/me",
		Summary:     "The signed-in user and their effective role on every visible project",
		Tags:        []string{"auth"},
	}, func(ctx context.Context, _ *struct{}) (*meOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		me, err := s.deps.Auth.Me(ctx, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &meOutput{Body: meBody(me)}, nil
	})
}

func meBody(me *auth.Me) MeBody {
	body := MeBody{
		User: UserSummary{
			ID:          me.Principal.UserID,
			Email:       me.Principal.Email,
			DisplayName: me.Principal.DisplayName,
			Role:        string(me.Principal.GlobalRole),
			IsActive:    me.Principal.IsActive,
		},
		Projects: []ProjectRoleSummary{},
	}
	for _, pr := range me.Projects {
		summary := ProjectRoleSummary{
			ProjectID:   pr.ProjectID,
			ProjectKey:  pr.ProjectKey,
			ProjectName: pr.ProjectName,
			Effective:   string(pr.Effective),
		}
		if pr.Override != nil {
			o := string(*pr.Override)
			summary.Override = &o
		}
		body.Projects = append(body.Projects, summary)
	}
	return body
}

// mapErr funnels every handler failure through the single mapping.
func (s *Server) mapErr(ctx context.Context, err error) error {
	return mapError(ctx, s.deps.Log, err)
}

// requireRole is the coarse check for endpoints the contract marks as
// admin-only. Field-level authority is never decided here: that is the
// ownership table's job, and duplicating it in a handler is how the two drift.
func requireRole(ctx context.Context, want policy.Role) (policy.Principal, error) {
	p, err := requirePrincipal(ctx)
	if err != nil {
		return p, err
	}
	if !p.GlobalRole.AtLeast(want) {
		return p, Errorf(CodeForbidden, "To do this, ask an %s. Your role is %s.", want, p.GlobalRole).
			WithDetail("owning_role", string(want)).
			WithDetail("effective_role", string(p.GlobalRole)).
			WithDetail("route", "ask an "+string(want)).
			WithRequestID(requestID(ctx))
	}
	return p, nil
}

var _ = time.Time{}
