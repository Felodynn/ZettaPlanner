package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/service/project"
)

// ProjectSummary is a project as the API presents one.
type ProjectSummary struct {
	ID          uuid.UUID      `json:"id"`
	Key         string         `json:"key" doc:"The prefix of every work item ref in this project, such as ZP in ZP-14"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Status      string         `json:"status" enum:"active,archived"`
	Settings    map[string]any `json:"settings"`
	GitEnabled  bool           `json:"git_enabled" doc:"Whether the one-way git projection is on. Off by default (D10)"`
}

// MemberSummary is one project member with their resolved role.
type MemberSummary struct {
	UserID      uuid.UUID `json:"user_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	GlobalRole  string    `json:"global_role" enum:"admin,manager,developer"`
	ProjectRole string    `json:"project_role" enum:"admin,manager,developer" doc:"The per-project override"`
	// Effective is what governs writes: the override, except that an admin is
	// always an admin.
	EffectiveRole string `json:"effective_role" enum:"admin,manager,developer"`
	IsActive      bool   `json:"is_active"`
}

// projectPatchBody is the PATCH body.
//
// Every field is a pointer: nil means absent, non-nil means write this. That is
// what policy.Fields reads to produce the set the ownership table is asked
// about, and it is why no handler here names a field.
type projectPatchBody struct {
	Name        *string         `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	Status      *string         `json:"status,omitempty" enum:"active,archived"`
	Settings    *map[string]any `json:"settings,omitempty"`
	GitEnabled  *bool           `json:"git_enabled,omitempty"`
}

type projectListOutput struct {
	Body struct {
		Projects []ProjectSummary `json:"projects"`
	}
}

type projectOutput struct {
	Body ProjectSummary
}

type memberListOutput struct {
	Body struct {
		Members []MemberSummary `json:"members"`
	}
}

func (s *Server) registerProjects() {
	huma.Register(s.API, huma.Operation{
		OperationID: "list-projects",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects",
		Summary:     "Projects the caller can see",
		Description: "An admin sees every project; anyone else sees the ones they are a member of.",
		Tags:        []string{"projects"},
	}, func(ctx context.Context, _ *struct{}) (*projectListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		projects, err := s.deps.Projects.List(ctx, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &projectListOutput{}
		out.Body.Projects = []ProjectSummary{}
		for _, proj := range projects {
			out.Body.Projects = append(out.Body.Projects, projectSummary(proj))
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "create-project",
		Method:        http.MethodPost,
		Path:          "/api/v1/projects",
		Summary:       "Create a project",
		Description:   "Admin only. The creator is added as a manager of the new project in the same operation.",
		Tags:          []string{"projects"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		Body struct {
			Key         string `json:"key" minLength:"1" maxLength:"10" pattern:"^[A-Z0-9]+$" doc:"Upper-case letters and digits; prefixes every work item ref"`
			Name        string `json:"name" minLength:"1"`
			Description string `json:"description,omitempty"`
		}
	}) (*projectOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		proj, err := s.deps.Projects.Create(ctx, project.CreateInput{
			Key:         in.Body.Key,
			Name:        in.Body.Name,
			Description: in.Body.Description,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &projectOutput{Body: projectSummary(proj)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "get-project",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}",
		Summary:     "One project",
		Description: "A caller who is not a member gets not_found rather than forbidden: distinguishing them would let anyone enumerate which projects exist.",
		Tags:        []string{"projects"},
	}, func(ctx context.Context, in *struct {
		PID uuid.UUID `path:"pid"`
	}) (*projectOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		proj, err := s.deps.Projects.Get(ctx, in.PID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &projectOutput{Body: projectSummary(proj)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "update-project",
		Method:      http.MethodPatch,
		Path:        "/api/v1/projects/{pid}",
		Summary:     "Change a project",
		Description: "Every field in the body is resolved to its owning role before anything is written (CONTRACT §4.2). " +
			"Project settings are manager-owned, so a developer gets field_not_owned naming the route forward.",
		Tags: []string{"projects"},
	}, func(ctx context.Context, in *struct {
		PID  uuid.UUID `path:"pid"`
		Body projectPatchBody
	}) (*projectOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}

		fields := policy.Fields(in.Body)
		if len(fields) == 0 {
			return nil, s.mapErr(ctx, ValidationFailed(
				"The request body set no fields. Send at least one of %v.",
				policy.PatchFieldNames(projectPatchBody{})))
		}

		proj, err := s.deps.Projects.Update(ctx, in.PID, project.UpdateInput{
			Fields:      fields,
			Name:        in.Body.Name,
			Description: in.Body.Description,
			Status:      in.Body.Status,
			Settings:    in.Body.Settings,
			GitEnabled:  in.Body.GitEnabled,
		}, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return &projectOutput{Body: projectSummary(proj)}, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID: "list-project-members",
		Method:      http.MethodGet,
		Path:        "/api/v1/projects/{pid}/members",
		Summary:     "Project members and their effective roles",
		Tags:        []string{"projects"},
	}, func(ctx context.Context, in *struct {
		PID uuid.UUID `path:"pid"`
	}) (*memberListOutput, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		rows, err := s.deps.Projects.Members(ctx, in.PID, p)
		if err != nil {
			return nil, s.mapErr(ctx, err)
		}
		out := &memberListOutput{}
		out.Body.Members = []MemberSummary{}
		for _, r := range rows {
			out.Body.Members = append(out.Body.Members, MemberSummary{
				UserID:        r.UserID,
				Email:         r.Email,
				DisplayName:   r.DisplayName,
				GlobalRole:    string(r.GlobalRole),
				ProjectRole:   string(r.ProjectRole),
				EffectiveRole: string(r.EffectiveRole),
				IsActive:      r.IsActive,
			})
		}
		return out, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "put-project-member",
		Method:        http.MethodPut,
		Path:          "/api/v1/projects/{pid}/members/{uid}",
		Summary:       "Add a member or change their project role",
		Description:   "Admin only. The project role overrides the user's global role for this project, including downward.",
		Tags:          []string{"projects"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		PID  uuid.UUID `path:"pid"`
		UID  uuid.UUID `path:"uid"`
		Body struct {
			Role string `json:"role" enum:"admin,manager,developer"`
		}
	}) (*struct{}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		role, err := policy.ParseRole(in.Body.Role)
		if err != nil {
			return nil, s.mapErr(ctx, ValidationFailed("%s", err.Error()))
		}
		if err := s.deps.Projects.PutMember(ctx, in.PID, in.UID, role, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return nil, nil
	})

	huma.Register(s.API, huma.Operation{
		OperationID:   "remove-project-member",
		Method:        http.MethodDelete,
		Path:          "/api/v1/projects/{pid}/members/{uid}",
		Summary:       "Remove a member",
		Description:   "Admin only. The membership row is removed; the add and the removal both stay in the audit log.",
		Tags:          []string{"projects"},
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		PID uuid.UUID `path:"pid"`
		UID uuid.UUID `path:"uid"`
	}) (*struct{}, error) {
		p, err := requirePrincipal(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.deps.Projects.RemoveMember(ctx, in.PID, in.UID, p); err != nil {
			return nil, s.mapErr(ctx, err)
		}
		return nil, nil
	})
}

func projectSummary(p *domain.Project) ProjectSummary {
	settings := p.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	return ProjectSummary{
		ID:          p.ID,
		Key:         p.Key,
		Name:        p.Name,
		Description: p.Description,
		Status:      p.Status,
		Settings:    settings,
		GitEnabled:  p.GitEnabled,
	}
}
