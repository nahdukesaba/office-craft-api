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

// Upsert creates or updates the profile row for a Supabase auth user.
// Used right after a successful register/login to keep app_users in sync.
func (r *UserRepository) Upsert(ctx context.Context, id, email, fullName, role string) (*models.AppUser, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO public.app_users (id, email, full_name, role)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE
		SET email = EXCLUDED.email,
		    full_name = CASE WHEN public.app_users.full_name = '' THEN EXCLUDED.full_name ELSE public.app_users.full_name END
		RETURNING id, email, full_name, role, created_at, updated_at
	`, id, email, fullName, role)

	var u models.AppUser
	if err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (*models.AppUser, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, email, full_name, role, created_at, updated_at
		FROM public.app_users WHERE id = $1
	`, id)

	var u models.AppUser
	if err := row.Scan(&u.ID, &u.Email, &u.FullName, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (r *UserRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.app_users`).Scan(&count)
	return count, err
}
