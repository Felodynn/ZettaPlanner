package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/auth"
)

type userListOutput struct {
	Body struct {
		Users []UserSummary `json:"users"`
	}
}

type userOutput struct {
	Body UserSummary
}

func (s *Server) registerUsers() {
	huma.Register(s.API, huma.Operation{
		OperationID: "list-users",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/users",
		Summary:     "Every account",
		Description: "Admin only.",
		Tags:        []string{"admin"},
	}, func(ctx context.Context, _ *struct{}) (*userListOutput, error) {
		p, err := requireRole(ctx, policy.RoleAdmin)
		if err != nil {
			return nil, err
		}
		users, err := s.deps.Auth.ListUsers(ctx, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &userListOutput{}
		out.Body.Users = []UserSummary{}
		for _, u := range users {
			out.Body.Users = append(out.Body.Users, userSummary(u))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "create-user",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/users",
		Summary:     "Create an account",
		Description: "Admin only. The account's actor row is created in the same operation, so an audit entry " +
			"can never reference a principal that does not exist.",
		Tags:          []string{"admin"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		Body struct {
			Email       string `json:"email" format:"email"`
			DisplayName string `json:"display_name" minLength:"1"`
			Password    string `json:"password" minLength:"12" doc:"At least 12 characters. Length, not character classes."`
			Role        string `json:"role" enum:"admin,manager,developer"`
			IsActive    *bool  `json:"is_active,omitempty"`
		}
	}) (*userOutput, error) {
		p, err := requireRole(ctx, policy.RoleAdmin)
		if err != nil {
			return nil, err
		}
		role, err := policy.ParseRole(in.Body.Role)
		if err != nil {
			return nil, s.mapErr(ctx, ValidationFailed("%s", err.Error()))
		}
		user, err := s.deps.Auth.CreateUser(ctx, auth.CreateUserInput{
			Email:       in.Body.Email,
			DisplayName: in.Body.DisplayName,
			Password:    in.Body.Password,
			Role:        role,
			IsActive:    in.Body.IsActive,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &userOutput{Body: userSummary(user)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "get-user",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/users/{id}",
		Summary:     "One account",
		Description: "Admin only, except that anyone may read their own.",
		Tags:        []string{"admin"},
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*userOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		user, err := s.deps.Auth.UserByID(ctx, in.ID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &userOutput{Body: userSummary(user)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "update-user",
		Method:      http.MethodPatch,
		Path:        "/api/v1/admin/users/{id}",
		Summary:     "Change an account",
		Description: "Admin only. Changing the role, the password, or deactivating the account revokes every " +
			"live session it holds: a session must not outlast the authority it was issued under.",
		Tags: []string{"admin"},
	}, func(ctx context.Context, in *struct {
		ID   uuid.UUID `path:"id"`
		Body struct {
			DisplayName *string `json:"display_name,omitempty"`
			Role        *string `json:"role,omitempty" enum:"admin,manager,developer"`
			IsActive    *bool   `json:"is_active,omitempty"`
			Password    *string `json:"password,omitempty" minLength:"12"`
		}
	}) (*userOutput, error) {
		p, err := requireRole(ctx, policy.RoleAdmin)
		if err != nil {
			return nil, err
		}

		patch := auth.UpdateUserInput{
			DisplayName: in.Body.DisplayName,
			IsActive:    in.Body.IsActive,
			Password:    in.Body.Password,
		}
		if in.Body.Role != nil {
			role, err := policy.ParseRole(*in.Body.Role)
			if err != nil {
				return nil, s.mapErr(ctx, ValidationFailed("%s", err.Error()))
			}
			patch.Role = &role
		}

		user, err := s.deps.Auth.UpdateUser(ctx, in.ID, patch, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &userOutput{Body: userSummary(user)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "delete-user",
		Method:      http.MethodDelete,
		Path:        "/api/v1/admin/users/{id}",
		Summary:     "Remove an account",
		Description: "Admin only. Soft delete: the row is marked deleted and its sessions revoked, so every " +
			"audit entry naming this account stays joinable (CONTRACT §6.5).",
		Tags:          []string{"admin"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{}, error) {
		p, err := requireRole(ctx, policy.RoleAdmin)
		if err != nil {
			return nil, err
		}
		if err := s.deps.Auth.DeleteUser(ctx, in.ID, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return nil, nil
	})
}

func userSummary(u *domain.User) UserSummary {
	return UserSummary{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Role:        string(u.Role),
		IsActive:    u.IsActive,
	}
}
