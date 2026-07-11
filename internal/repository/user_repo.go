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

const userColumns = `id, email, full_name, phone, role, status, created_at, updated_at`

func scanUser(row pgx.Row) (*models.AppUser, error) {
	var u models.AppUser
	if err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.Phone, &u.Role, &u.Status, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

// Upsert creates or updates the profile row for a Supabase auth user, used
// right after register/login to keep app_users in sync. `status` is only
// applied on first insert (via INSERT ... status) - it is intentionally NOT
// overwritten on conflict, so re-logging-in never silently resets an
// admin's approve/reject decision back to its original value. `phone` may
// be nil (not supplied at registration); once set, it's likewise preserved
// across future upserts rather than getting blanked out.
func (r *UserRepository) Upsert(ctx context.Context, id, email, fullName string, phone *string, role, status string) (*models.AppUser, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.app_users (id, email, full_name, phone, role, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE
		SET email = EXCLUDED.email,
		    full_name = CASE WHEN public.app_users.full_name = '' THEN EXCLUDED.full_name ELSE public.app_users.full_name END,
		    phone = CASE WHEN public.app_users.phone IS NULL THEN EXCLUDED.phone ELSE public.app_users.phone END
		RETURNING `+userColumns, id, email, fullName, phone, role, status)

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

// ListAdmins returns every active (approved, not deleted) admin - used to
// fan out the "new booking request / started / finished" notifications to
// whoever should review them.
func (r *UserRepository) ListAdmins(ctx context.Context) ([]models.AppUser, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+userColumns+`
		FROM public.app_users
		WHERE deleted_at IS NULL AND role = 'admin' AND status = 'approved'
		ORDER BY created_at ASC
	`)
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

// UpdatePhone lets a user set/change their own WhatsApp number. No route
// wires this up yet (out of scope for the notify feature itself) - it's
// here so adding a "my profile" endpoint later is a one-handler change.
func (r *UserRepository) UpdatePhone(ctx context.Context, id, phone string) (*models.AppUser, error) {
	row := r.pool.QueryRow(ctx, `
		UPDATE public.app_users SET phone = $1 WHERE id = $2 AND deleted_at IS NULL
		RETURNING `+userColumns, phone, id)
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
