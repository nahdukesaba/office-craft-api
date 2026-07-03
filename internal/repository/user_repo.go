package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/models"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

const userColumns = `id, email, full_name, role, status, created_at, updated_at`

func scanUser(row pgx.Row) (*models.AppUser, error) {
	var u models.AppUser
	if err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.Status, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// Upsert creates or updates the profile row for a Supabase auth user, used
// right after register/login to keep app_users in sync. `status` is only
// applied on first insert (via INSERT ... status) - it is intentionally NOT
// overwritten on conflict, so re-logging-in never silently resets an
// admin's approve/reject decision back to its original value.
func (r *UserRepository) Upsert(ctx context.Context, id, email, fullName, role, status string) (*models.AppUser, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.app_users (id, email, full_name, role, status)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE
		SET email = EXCLUDED.email,
		    full_name = CASE WHEN public.app_users.full_name = '' THEN EXCLUDED.full_name ELSE public.app_users.full_name END
		RETURNING `+userColumns, id, email, fullName, role, status)

	return scanUser(row)
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (*models.AppUser, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM public.app_users WHERE id = $1 AND deleted_at IS NULL`, id)
	u, err := scanUser(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

func (r *UserRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.app_users WHERE deleted_at IS NULL`).Scan(&count)
	return count, err
}

// List returns users, optionally filtered by status ("" means no filter).
func (r *UserRepository) List(ctx context.Context, status string) ([]models.AppUser, error) {
	query := `SELECT ` + userColumns + ` FROM public.app_users WHERE deleted_at IS NULL`
	args := []interface{}{}
	if status != "" {
		query += ` AND status = $1`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.AppUser
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UpdateStatus is used by the admin approve/reject endpoints.
func (r *UserRepository) UpdateStatus(ctx context.Context, id, status string) (*models.AppUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE public.app_users SET status = $1 WHERE id = $2 AND deleted_at IS NULL
		RETURNING `+userColumns, status, id)
	u, err := scanUser(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// SoftDelete marks a user as deleted without removing the row. No route
// currently calls this (there's no "delete user" endpoint yet), but it's
// here so the repository is ready if/when one is added.
func (r *UserRepository) SoftDelete(ctx context.Context, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE public.app_users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
