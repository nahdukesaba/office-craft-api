package repository

import (
	"context"
	"fmt"

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

const silapetUserColumns = `
	u.id,
	u.email,
	u.full_name,
	u.phone,
	m.role,
	m.status,
	u.created_at,
	u.updated_at
`

const silapetUserJoin = `
	FROM public.app_users u
	JOIN public.app_memberships m
		ON m.app_user_id = u.id
		AND m.deleted_at IS NULL
	JOIN public.applications a
		ON a.id = m.application_id
		AND a.is_active = TRUE
`

func scanUser(row pgx.Row) (*models.AppUser, error) {
	var u models.AppUser

	if err := row.Scan(
		&u.ID,
		&u.Email,
		&u.FullName,
		&u.Phone,
		&u.Role,
		&u.Status,
		&u.CreatedAt,
		&u.UpdatedAt,
	); err != nil {
		return nil, err
	}

	return &u, nil
}

// Upsert creates/updates the central identity profile and ensures a SILAPET
// membership exists.
//
// role/status are only used when the SILAPET membership is first created.
// Existing membership authorization is never overwritten by login/register
// upserts.
//
// app_users.role/status are still populated on INSERT for compatibility with
// the legacy schema, but they are no longer the SILAPET authorization source.
func (r *UserRepository) Upsert(
	ctx context.Context,
	id, email, fullName string,
	phone *string,
	role, status string,
) (*models.AppUser, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var applicationID string
	if err := tx.QueryRow(
		ctx,
		`
			SELECT id
			FROM public.applications
			WHERE code = $1
			  AND is_active = TRUE
		`,
		models.ApplicationCodeSilapet,
	).Scan(&applicationID); err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("SILAPET application is not configured")
		}
		return nil, err
	}

	_, err = tx.Exec(
		ctx,
		`
			INSERT INTO public.app_users (
				id,
				email,
				full_name,
				phone,
				role,
				status
			)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (id) DO UPDATE
			SET email = EXCLUDED.email,
			    full_name = CASE
			        WHEN public.app_users.full_name = ''
			        THEN EXCLUDED.full_name
			        ELSE public.app_users.full_name
			    END,
			    phone = CASE
			        WHEN public.app_users.phone IS NULL
			        THEN EXCLUDED.phone
			        ELSE public.app_users.phone
			    END
		`,
		id,
		email,
		fullName,
		phone,
		role,
		status,
	)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(
		ctx,
		`
			INSERT INTO public.app_memberships (
				app_user_id,
				application_id,
				role,
				status
			)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (app_user_id, application_id) DO NOTHING
		`,
		id,
		applicationID,
		role,
		status,
	)
	if err != nil {
		return nil, err
	}

	user, err := scanUser(
		tx.QueryRow(
			ctx,
			`SELECT `+silapetUserColumns+silapetUserJoin+`
			 WHERE u.id = $1
			   AND u.deleted_at IS NULL
			   AND a.code = $2
			`,
			id,
			models.ApplicationCodeSilapet,
		),
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf(
				"SILAPET membership is unavailable for user %s",
				id,
			)
		}
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return user, nil
}

// CreateMembership creates only SILAPET application authorization for an
// existing central identity. It never creates or updates public.app_users.
//
// The bool result is true only when a new membership was inserted. Existing
// memberships are returned unchanged so retries cannot silently reset role or
// approval status.
func (r *UserRepository) CreateMembership(
	ctx context.Context,
	userID string,
	role string,
	status string,
) (*models.AppUser, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	var identityExists bool
	if err := tx.QueryRow(
		ctx,
		`
			SELECT EXISTS (
				SELECT 1
				FROM public.app_users
				WHERE id = $1
				  AND deleted_at IS NULL
			)
		`,
		userID,
	).Scan(&identityExists); err != nil {
		return nil, false, err
	}

	if !identityExists {
		return nil, false, fmt.Errorf(
			"central identity is unavailable for user %s",
			userID,
		)
	}

	var applicationID string
	if err := tx.QueryRow(
		ctx,
		`
			SELECT id
			FROM public.applications
			WHERE code = $1
			  AND is_active = TRUE
		`,
		models.ApplicationCodeSilapet,
	).Scan(&applicationID); err != nil {
		if err == pgx.ErrNoRows {
			return nil, false, fmt.Errorf(
				"SILAPET application is not configured",
			)
		}
		return nil, false, err
	}

	var membershipID string
	created := true

	err = tx.QueryRow(
		ctx,
		`
			INSERT INTO public.app_memberships (
				app_user_id,
				application_id,
				role,
				status
			)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (app_user_id, application_id) DO NOTHING
			RETURNING id
		`,
		userID,
		applicationID,
		role,
		status,
	).Scan(&membershipID)

	if err != nil {
		if err == pgx.ErrNoRows {
			created = false
		} else {
			return nil, false, err
		}
	}

	user, err := scanUser(
		tx.QueryRow(
			ctx,
			`SELECT `+silapetUserColumns+silapetUserJoin+`
			 WHERE u.id = $1
			   AND u.deleted_at IS NULL
			   AND a.code = $2
			`,
			userID,
			models.ApplicationCodeSilapet,
		),
	)
	if err != nil {
		return nil, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}

	return user, created, nil
}

func (r *UserRepository) GetByID(
	ctx context.Context,
	id string,
) (*models.AppUser, error) {
	user, err := scanUser(
		r.pool.QueryRow(
			ctx,
			`SELECT `+silapetUserColumns+silapetUserJoin+`
			 WHERE u.id = $1
			   AND u.deleted_at IS NULL
			   AND a.code = $2
			`,
			id,
			models.ApplicationCodeSilapet,
		),
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return user, nil
}

// GetAuthenticationContext reads all mutable authentication and authorization
// state needed by the protected-request middleware. It never creates or
// updates Supabase sessions, profiles, applications, or memberships.
func (r *UserRepository) GetAuthenticationContext(
	ctx context.Context,
	appUserID string,
	sessionID string,
	applicationCode string,
) (*models.AuthenticationContext, error) {
	var result models.AuthenticationContext

	err := r.pool.QueryRow(
		ctx,
		`
			SELECT
				u.id::text,
				COALESCE(u.auth_user_id::text, ''),
				u.email,
				u.deleted_at IS NOT NULL,
				s.id IS NOT NULL,
				COALESCE(s.user_id::text, ''),
				a.id IS NOT NULL,
				COALESCE(a.code, ''),
				COALESCE(a.is_active, FALSE),
				m.id IS NOT NULL,
				COALESCE(m.role, ''),
				COALESCE(m.status, ''),
				m.deleted_at IS NOT NULL
			FROM public.app_users u
			LEFT JOIN auth.sessions s
				ON s.id = $2::uuid
			LEFT JOIN public.applications a
				ON a.code = $3
			LEFT JOIN public.app_memberships m
				ON m.app_user_id = u.id
				AND m.application_id = a.id
			WHERE u.id = $1::uuid
		`,
		appUserID,
		sessionID,
		applicationCode,
	).Scan(
		&result.AppUserID,
		&result.AuthUserID,
		&result.Email,
		&result.UserDeleted,
		&result.SessionFound,
		&result.SessionUserID,
		&result.ApplicationFound,
		&result.ApplicationCode,
		&result.ApplicationActive,
		&result.MembershipFound,
		&result.MembershipRole,
		&result.MembershipStatus,
		&result.MembershipDeleted,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &result, nil
}

func (r *UserRepository) GetByIDs(
	ctx context.Context,
	ids []string,
) (map[string]*models.AppUser, error) {
	out := make(map[string]*models.AppUser, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	rows, err := r.pool.Query(
		ctx,
		`SELECT `+silapetUserColumns+silapetUserJoin+`
		 WHERE u.id = ANY($1::uuid[])
		   AND u.deleted_at IS NULL
		   AND a.code = $2
		`,
		ids,
		models.ApplicationCodeSilapet,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u
	}

	return out, rows.Err()
}

func (r *UserRepository) Count(ctx context.Context) (int64, error) {
	var count int64

	err := r.pool.QueryRow(
		ctx,
		`
			SELECT COUNT(*)
			FROM public.app_users u
			JOIN public.app_memberships m
				ON m.app_user_id = u.id
				AND m.deleted_at IS NULL
			JOIN public.applications a
				ON a.id = m.application_id
				AND a.is_active = TRUE
			WHERE u.deleted_at IS NULL
			  AND a.code = $1
		`,
		models.ApplicationCodeSilapet,
	).Scan(&count)

	return count, err
}

// List returns SILAPET members, optionally filtered by membership status.
func (r *UserRepository) List(
	ctx context.Context,
	status string,
) ([]models.AppUser, error) {
	query := `SELECT ` + silapetUserColumns + silapetUserJoin + `
		WHERE u.deleted_at IS NULL
		  AND a.code = $1
	`

	args := []interface{}{models.ApplicationCodeSilapet}

	if status != "" {
		query += ` AND m.status = $2`
		args = append(args, status)
	}

	query += ` ORDER BY u.created_at DESC`

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

// UpdateStatus changes only the SILAPET membership status.
// app_users.status is intentionally left untouched.
func (r *UserRepository) UpdateStatus(
	ctx context.Context,
	id, status string,
) (*models.AppUser, error) {
	tag, err := r.pool.Exec(
		ctx,
		`
			UPDATE public.app_memberships m
			SET status = $1,
			    updated_at = now()
			FROM public.applications a
			WHERE m.application_id = a.id
			  AND a.code = $2
			  AND a.is_active = TRUE
			  AND m.app_user_id = $3
			  AND m.deleted_at IS NULL
		`,
		status,
		models.ApplicationCodeSilapet,
		id,
	)
	if err != nil {
		return nil, err
	}

	if tag.RowsAffected() == 0 {
		return nil, nil
	}

	return r.GetByID(ctx, id)
}

// ListAdmins returns approved SILAPET administrators.
// Role/status are sourced from app_memberships.
func (r *UserRepository) ListAdmins(
	ctx context.Context,
) ([]models.AppUser, error) {
	rows, err := r.pool.Query(
		ctx,
		`SELECT `+silapetUserColumns+silapetUserJoin+`
		 WHERE u.deleted_at IS NULL
		   AND a.code = $1
		   AND m.role = $2
		   AND m.status = $3
		 ORDER BY u.created_at ASC
		`,
		models.ApplicationCodeSilapet,
		models.RoleAdmin,
		models.UserStatusApproved,
	)
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

// UpdatePhone changes the central profile field, then returns the SILAPET
// projection so role/status still come from app_memberships.
func (r *UserRepository) UpdatePhone(
	ctx context.Context,
	id, phone string,
) (*models.AppUser, error) {
	tag, err := r.pool.Exec(
		ctx,
		`
			UPDATE public.app_users
			SET phone = $1
			WHERE id = $2
			  AND deleted_at IS NULL
		`,
		phone,
		id,
	)
	if err != nil {
		return nil, err
	}

	if tag.RowsAffected() == 0 {
		return nil, nil
	}

	return r.GetByID(ctx, id)
}
