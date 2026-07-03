package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/models"
)

type ResourceRepository struct {
	pool *pgxpool.Pool
}

func NewResourceRepository(pool *pgxpool.Pool) *ResourceRepository {
	return &ResourceRepository{pool: pool}
}

type ResourceFilter struct {
	Search       string
	Type         string // room, car, bike, all
	Availability *bool
}

const resourceColumns = `
	id, type, name, description, location, photo_url, is_available,
	capacity, amenities, license_plate, seats, fuel_type, created_at, updated_at
`

func scanResource(row pgx.Row) (*models.Resource, error) {
	var r models.Resource
	var amenitiesRaw []byte
	if err := row.Scan(
		&r.ID, &r.Type, &r.Name, &r.Description, &r.Location, &r.PhotoURL, &r.IsAvailable,
		&r.Capacity, &amenitiesRaw, &r.LicensePlate, &r.Seats, &r.FuelType, &r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	var amenities []string
	if len(amenitiesRaw) > 0 {
		if err := json.Unmarshal(amenitiesRaw, &amenities); err != nil {
			return nil, fmt.Errorf("decoding amenities jsonb: %w", err)
		}
	}
	r.Amenities = amenities
	return &r, nil
}

// amenitiesJSON marshals amenities to a JSON array string. pgx has no
// built-in way to know a Go []string parameter should be encoded as JSON
// rather than a Postgres text[] array literal, so we do it explicitly and
// pass the result as a plain string - Postgres then does its normal
// text -> jsonb assignment cast on INSERT/UPDATE.
func amenitiesJSON(amenities []string) (string, error) {
	if amenities == nil {
		amenities = []string{}
	}
	b, err := json.Marshal(amenities)
	if err != nil {
		return "", fmt.Errorf("encoding amenities: %w", err)
	}
	return string(b), nil
}

func (r *ResourceRepository) List(ctx context.Context, f ResourceFilter) ([]models.Resource, error) {
	query := fmt.Sprintf(`SELECT %s FROM public.resources WHERE deleted_at IS NULL`, resourceColumns)
	args := []interface{}{}
	argN := 1

	if f.Search != "" {
		query += fmt.Sprintf(" AND (name ILIKE $%d OR description ILIKE $%d OR location ILIKE $%d)", argN, argN, argN)
		args = append(args, "%"+f.Search+"%")
		argN++
	}
	if f.Type != "" && strings.ToLower(f.Type) != "all" {
		query += fmt.Sprintf(" AND type = $%d", argN)
		args = append(args, f.Type)
		argN++
	}
	if f.Availability != nil {
		query += fmt.Sprintf(" AND is_available = $%d", argN)
		args = append(args, *f.Availability)
		argN++
	}
	query += " ORDER BY created_at DESC"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Resource
	for rows.Next() {
		res, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *res)
	}
	return out, rows.Err()
}

func (r *ResourceRepository) GetByID(ctx context.Context, id string) (*models.Resource, error) {
	query := fmt.Sprintf(`SELECT %s FROM public.resources WHERE id = $1 AND deleted_at IS NULL`, resourceColumns)
	row := r.pool.QueryRow(ctx, query, id)
	res, err := scanResource(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return res, nil
}

func (r *ResourceRepository) Create(ctx context.Context, in models.ResourceInput) (*models.Resource, error) {
	isAvailable := true
	if in.IsAvailable != nil {
		isAvailable = *in.IsAvailable
	}
	amenities, err := amenitiesJSON(in.Amenities)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		INSERT INTO public.resources
			(type, name, description, location, photo_url, is_available, capacity, amenities, license_plate, seats, fuel_type)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING %s
	`, resourceColumns)

	row := r.pool.QueryRow(ctx, query,
		in.Type, in.Name, in.Description, in.Location, in.PhotoURL, isAvailable,
		in.Capacity, amenities, in.LicensePlate, in.Seats, in.FuelType,
	)
	return scanResource(row)
}

func (r *ResourceRepository) Update(ctx context.Context, id string, in models.ResourceInput) (*models.Resource, error) {
	isAvailable := true
	if in.IsAvailable != nil {
		isAvailable = *in.IsAvailable
	}
	amenities, err := amenitiesJSON(in.Amenities)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
		UPDATE public.resources SET
			type = $1, name = $2, description = $3, location = $4, photo_url = $5,
			is_available = $6, capacity = $7, amenities = $8, license_plate = $9,
			seats = $10, fuel_type = $11
		WHERE id = $12 AND deleted_at IS NULL
		RETURNING %s
	`, resourceColumns)

	row := r.pool.QueryRow(ctx, query,
		in.Type, in.Name, in.Description, in.Location, in.PhotoURL, isAvailable,
		in.Capacity, amenities, in.LicensePlate, in.Seats, in.FuelType, id,
	)
	res, err := scanResource(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return res, nil
}

// Delete soft-deletes a resource: the row stays in place (and so do every
// booking/proof/event that ever referenced it, for audit purposes) with
// deleted_at set, and is filtered out of every normal read from here on.
func (r *ResourceRepository) Delete(ctx context.Context, id string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE public.resources SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *ResourceRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.resources WHERE deleted_at IS NULL`).Scan(&count)
	return count, err
}
