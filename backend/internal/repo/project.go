package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Projects is the projects aggregate.
type Projects struct{}

const projectColumns = `id, key, name, description, status, settings, git_enabled,
                        created_at, updated_at, deleted_at`

// Create inserts a project.
func (Projects) Create(ctx context.Context, q store.Querier, p *domain.Project) error {
	settings, err := json.Marshal(orEmpty(p.Settings))
	if err != nil {
		return err
	}
	const sql = `
		INSERT INTO projects (id, key, name, description, status, settings, git_enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`
	_, err = q.Exec(ctx, sql,
		p.ID, p.Key, p.Name, nullIfEmpty(p.Description), p.Status, settings, p.GitEnabled, p.CreatedAt)
	return conflict(err, "a project with that key already exists")
}

// ByID returns a live project.
func (Projects) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*domain.Project, error) {
	const sql = `SELECT ` + projectColumns + ` FROM projects WHERE id = $1 AND deleted_at IS NULL`
	return scanProject(q.QueryRow(ctx, sql, id))
}

// ByKey returns a live project by its human-facing key.
func (Projects) ByKey(ctx context.Context, q store.Querier, key string) (*domain.Project, error) {
	const sql = `SELECT ` + projectColumns + ` FROM projects WHERE key = $1 AND deleted_at IS NULL`
	return scanProject(q.QueryRow(ctx, sql, key))
}

// ListVisible returns the projects a user may see.
//
// An admin sees every project; anyone else sees the ones they are a member of.
// Filtering here rather than in the handler means a missing check cannot leak a
// project list.
func (Projects) ListVisible(ctx context.Context, q store.Querier, userID uuid.UUID, isAdmin bool) ([]*domain.Project, error) {
	const sql = `
		SELECT ` + projectColumns + `
		  FROM projects p
		 WHERE p.deleted_at IS NULL
		   AND ($2 OR EXISTS (
		         SELECT 1 FROM project_members m
		          WHERE m.project_id = p.id AND m.user_id = $1))
		 ORDER BY p.created_at`
	rows, err := q.Query(ctx, sql, userID, isAdmin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Update writes the mutable fields of a project.
func (Projects) Update(ctx context.Context, q store.Querier, p *domain.Project) error {
	settings, err := json.Marshal(orEmpty(p.Settings))
	if err != nil {
		return err
	}
	const sql = `
		UPDATE projects
		   SET name = $2, description = $3, status = $4, settings = $5, git_enabled = $6, updated_at = $7
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, p.ID, p.Name, nullIfEmpty(p.Description), p.Status, settings, p.GitEnabled, time.Now())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("project")
	}
	return nil
}

// Delete soft-deletes a project.
func (Projects) Delete(ctx context.Context, q store.Querier, id uuid.UUID) error {
	const sql = `UPDATE projects SET deleted_at = now(), updated_at = now()
	              WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("project")
	}
	return nil
}

func scanProject(row scanner) (*domain.Project, error) {
	var (
		p           domain.Project
		description *string
		settings    []byte
	)
	err := row.Scan(&p.ID, &p.Key, &p.Name, &description, &p.Status, &settings,
		&p.GitEnabled, &p.CreatedAt, &p.UpdatedAt, &p.DeletedAt)
	if err != nil {
		return nil, notFound(err, "project")
	}
	if description != nil {
		p.Description = *description
	}
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &p.Settings); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

// ── membership ───────────────────────────────────────────────────────────────

// Members is the project_members aggregate.
type Members struct{}

// Put inserts or updates a membership.
func (Members) Put(ctx context.Context, q store.Querier, m domain.ProjectMember) error {
	const sql = `
		INSERT INTO project_members (project_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role`
	_, err := q.Exec(ctx, sql, m.ProjectID, m.UserID, string(m.Role))
	return err
}

// Remove drops a membership.
//
// The only row removal in this package, and it is deliberate rather than an
// oversight. `project_members` is not in CONTRACT §6.5 constraint 1's list of
// soft-deleted tables, because a membership is current state rather than
// history: the audit log records the add and the remove as their own entries,
// so nothing is lost. A soft-deleted membership row would instead have to be
// filtered out of every permission check for the rest of the product's life,
// and that is the sort of filter that gets forgotten exactly once.
//
// The marker on the statement is what the no-hard-delete test requires: a
// removal is permitted only where it is annotated and the table is not one the
// contract says carries deleted_at.
func (Members) Remove(ctx context.Context, q store.Querier, projectID, userID uuid.UUID) error {
	const sql = `DELETE FROM project_members WHERE project_id = $1 AND user_id = $2` // zp:hard-delete-ok
	tag, err := q.Exec(ctx, sql, projectID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("project member")
	}
	return nil
}

// RoleFor returns a user's project-level role override, or nil if they are not
// a member. Absence is not an error: "not a member" is a normal answer that the
// policy layer needs in order to deny with the right message.
func (Members) RoleFor(ctx context.Context, q store.Querier, projectID, userID uuid.UUID) (*policy.Role, error) {
	const sql = `SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`
	var role string
	err := q.QueryRow(ctx, sql, projectID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r := policy.Role(role)
	return &r, nil
}

// List returns a project's members with their overrides, joined to the user.
func (Members) List(ctx context.Context, q store.Querier, projectID uuid.UUID) ([]MemberRow, error) {
	const sql = `
		SELECT m.user_id, m.role, u.email, u.display_name, u.role, u.is_active
		  FROM project_members m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.project_id = $1 AND u.deleted_at IS NULL
		 ORDER BY u.display_name`
	rows, err := q.Query(ctx, sql, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MemberRow
	for rows.Next() {
		var (
			r                     MemberRow
			projectRole, userRole string
		)
		if err := rows.Scan(&r.UserID, &projectRole, &r.Email, &r.DisplayName, &userRole, &r.IsActive); err != nil {
			return nil, err
		}
		r.ProjectRole = policy.Role(projectRole)
		r.GlobalRole = policy.Role(userRole)
		r.EffectiveRole = policy.Effective(r.GlobalRole, &r.ProjectRole)
		out = append(out, r)
	}
	return out, rows.Err()
}

// MemberRow is a membership joined to its user.
type MemberRow struct {
	UserID        uuid.UUID
	Email         string
	DisplayName   string
	GlobalRole    policy.Role
	ProjectRole   policy.Role
	EffectiveRole policy.Role
	IsActive      bool
}

// ProjectRoles returns every project role a user holds, keyed by project id.
// One query, so `auth/me` does not fan out per project.
func (Members) ProjectRoles(ctx context.Context, q store.Querier, userID uuid.UUID) (map[uuid.UUID]policy.Role, error) {
	const sql = `
		SELECT m.project_id, m.role
		  FROM project_members m
		  JOIN projects p ON p.id = m.project_id AND p.deleted_at IS NULL
		 WHERE m.user_id = $1`
	rows, err := q.Query(ctx, sql, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID]policy.Role{}
	for rows.Next() {
		var (
			id   uuid.UUID
			role string
		)
		if err := rows.Scan(&id, &role); err != nil {
			return nil, err
		}
		out[id] = policy.Role(role)
	}
	return out, rows.Err()
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
