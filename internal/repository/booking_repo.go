package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/models"
)

type BookingRepository struct {
	pool *pgxpool.Pool
}

func NewBookingRepository(pool *pgxpool.Pool) *BookingRepository {
	return &BookingRepository{pool: pool}
}

type BookingFilter struct {
	Status     string
	ResourceID string
	UserID     string
	From       *time.Time
	To         *time.Time
	Page       int
	PageSize   int
}

const bookingColumns = `
	b.id, b.resource_id, b.user_id, b.start_time, b.end_time, b.status, b.purpose, b.created_at, b.updated_at
`

func scanBooking(row pgx.Row) (*models.Booking, error) {
	var b models.Booking
	if err := row.Scan(&b.ID, &b.ResourceID, &b.UserID, &b.StartTime, &b.EndTime, &b.Status, &b.Purpose, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

// HasConflict returns true if an overlapping pending/approved booking exists
// for the given resource. excludeBookingID can be empty when creating a new booking.
func (r *BookingRepository) HasConflict(ctx context.Context, resourceID string, start, end time.Time, excludeBookingID string) (bool, error) {
	query := `
		SELECT EXISTS (
			SELECT 1 FROM public.bookings
			WHERE resource_id = $1
			  AND status IN ('pending', 'approved')
			  AND start_time < $3
			  AND end_time > $2
			  AND ($4 = '' OR id != $4::uuid)
		)
	`
	var exists bool
	err := r.pool.QueryRow(ctx, query, resourceID, start, end, excludeBookingID).Scan(&exists)
	return exists, err
}

func (r *BookingRepository) Create(ctx context.Context, userID string, in models.BookingInput) (*models.Booking, error) {
	query := fmt.Sprintf(`
		INSERT INTO public.bookings (resource_id, user_id, start_time, end_time, status, purpose)
		VALUES ($1, $2, $3, $4, 'pending', $5)
		RETURNING %s
	`, bookingColumns)
	row := r.pool.QueryRow(ctx, query, in.ResourceID, userID, in.StartTime, in.EndTime, in.Purpose)
	return scanBooking(row)
}

func (r *BookingRepository) GetByID(ctx context.Context, id string) (*models.Booking, error) {
	query := fmt.Sprintf(`SELECT %s FROM public.bookings b WHERE b.id = $1`, bookingColumns)
	row := r.pool.QueryRow(ctx, query, id)
	b, err := scanBooking(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

func (r *BookingRepository) SetStatus(ctx context.Context, id, status string) (*models.Booking, error) {
	query := fmt.Sprintf(`
		UPDATE public.bookings SET status = $1 WHERE id = $2
		RETURNING %s
	`, bookingColumns)
	row := r.pool.QueryRow(ctx, query, status, id)
	b, err := scanBooking(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

func (r *BookingRepository) List(ctx context.Context, f BookingFilter) ([]models.Booking, int64, error) {
	where := "WHERE 1=1"
	args := []interface{}{}
	argN := 1

	if f.Status != "" {
		where += fmt.Sprintf(" AND b.status = $%d", argN)
		args = append(args, f.Status)
		argN++
	}
	if f.ResourceID != "" {
		where += fmt.Sprintf(" AND b.resource_id = $%d", argN)
		args = append(args, f.ResourceID)
		argN++
	}
	if f.UserID != "" {
		where += fmt.Sprintf(" AND b.user_id = $%d", argN)
		args = append(args, f.UserID)
		argN++
	}
	if f.From != nil {
		where += fmt.Sprintf(" AND b.end_time >= $%d", argN)
		args = append(args, *f.From)
		argN++
	}
	if f.To != nil {
		where += fmt.Sprintf(" AND b.start_time <= $%d", argN)
		args = append(args, *f.To)
		argN++
	}

	var total int64
	countQuery := "SELECT COUNT(*) FROM public.bookings b " + where
	if err := r.pool.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	page := f.Page
	if page < 1 {
		page = 1
	}
	pageSize := f.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	query := fmt.Sprintf(`SELECT %s FROM public.bookings b %s ORDER BY b.start_time DESC LIMIT $%d OFFSET $%d`,
		bookingColumns, where, argN, argN+1)
	args = append(args, pageSize, offset)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []models.Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *b)
	}
	return out, total, rows.Err()
}

// ListPublic returns all bookings (optionally scoped to a resource) without
// pagination, for unauthenticated calendar/availability views. Only minimal
// fields are exposed by the handler layer.
func (r *BookingRepository) ListPublic(ctx context.Context, resourceID string) ([]models.Booking, error) {
	query := fmt.Sprintf(`SELECT %s FROM public.bookings b WHERE b.status IN ('pending','approved','completed')`, bookingColumns)
	args := []interface{}{}
	if resourceID != "" {
		query += " AND b.resource_id = $1"
		args = append(args, resourceID)
	}
	query += " ORDER BY b.start_time ASC"

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Booking
	for rows.Next() {
		b, err := scanBooking(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (r *BookingRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT status, COUNT(*) FROM public.bookings GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		out[status] = count
	}
	return out, rows.Err()
}
