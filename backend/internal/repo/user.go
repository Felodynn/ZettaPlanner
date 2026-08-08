package repo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Felodynn/ZettaPlanner/backend/internal/domain"
	"github.com/Felodynn/ZettaPlanner/backend/internal/policy"
	"github.com/Felodynn/ZettaPlanner/backend/internal/store"
)

// Users is the users aggregate.
type Users struct{}

const userColumns = `id, email, display_name, password_hash, role, is_active,
                     created_at, updated_at, deleted_at`

// Create inserts a user. The caller supplies the id so that the audit entry and
// the row agree without a round trip.
func (Users) Create(ctx context.Context, q store.Querier, u *domain.User) error {
	const sql = `
		INSERT INTO users (id, email, display_name, password_hash, role, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`
	_, err := q.Exec(ctx, sql,
		u.ID, u.Email, u.DisplayName, u.PasswordHash, string(u.Role), u.IsActive, u.CreatedAt)
	return conflict(err, "a user with that email already exists")
}

// ByID returns a live user.
func (Users) ByID(ctx context.Context, q store.Querier, id uuid.UUID) (*domain.User, error) {
	const sql = `SELECT ` + userColumns + ` FROM users WHERE id = $1 AND deleted_at IS NULL`
	return scanUser(q.QueryRow(ctx, sql, id))
}

// DisplayNames resolves several users to their display names in one round trip.
//
// Context assembly names every assignee it prints. A model handed a UUID cannot
// say anything useful about who is doing the work, and neither can a person
// reading the assembled context to find out why the model said something.
func (Users) DisplayNames(ctx context.Context, q store.Querier, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return out, nil
	}
	const sql = `SELECT id, display_name FROM users WHERE id = ANY($1)`
	rows, err := q.Query(ctx, sql, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id   uuid.UUID
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// ByEmail returns a live user by normalised email.
func (Users) ByEmail(ctx context.Context, q store.Querier, email string) (*domain.User, error) {
	const sql = `SELECT ` + userColumns + ` FROM users WHERE email = $1 AND deleted_at IS NULL`
	return scanUser(q.QueryRow(ctx, sql, domain.NormaliseEmail(email)))
}

// List returns every live user, oldest first.
func (Users) List(ctx context.Context, q store.Querier) ([]*domain.User, error) {
	const sql = `SELECT ` + userColumns + ` FROM users WHERE deleted_at IS NULL ORDER BY created_at`
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Update writes the mutable fields of a user.
func (Users) Update(ctx context.Context, q store.Querier, u *domain.User) error {
	const sql = `
		UPDATE users
		   SET email = $2, display_name = $3, password_hash = $4, role = $5,
		       is_active = $6, updated_at = $7
		 WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql,
		u.ID, u.Email, u.DisplayName, u.PasswordHash, string(u.Role), u.IsActive, time.Now())
	if err != nil {
		return conflict(err, "a user with that email already exists")
	}
	if tag.RowsAffected() == 0 {
		return missing("user")
	}
	return nil
}

// Delete soft-deletes a user.
//
// Soft, always. The audit rows naming this user must stay joinable, and a
// deactivated account whose history has vanished is worse than no record at all.
func (Users) Delete(ctx context.Context, q store.Querier, id uuid.UUID) error {
	const sql = `UPDATE users SET deleted_at = now(), is_active = FALSE, updated_at = now()
	              WHERE id = $1 AND deleted_at IS NULL`
	tag, err := q.Exec(ctx, sql, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return missing("user")
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scanUser(row scanner) (*domain.User, error) {
	var (
		u    domain.User
		role string
	)
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.PasswordHash, &role,
		&u.IsActive, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt)
	if err != nil {
		return nil, notFound(err, "user")
	}
	u.Role = policy.Role(role)
	return &u, nil
}
