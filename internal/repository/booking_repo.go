package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/models"
	"office-craft-api/internal/utils"
)

type BookingRepository struct {
	pool *pgxpool.Pool
}

func NewBookingRepository(pool *pgxpool.Pool) *BookingRepository {
	return &BookingRepository{pool: pool}
}

var (
	// ErrBookingNotPending is returned by ApproveWithAutoReject when the
	// booking is no longer in "pending" status (e.g. a concurrent request
	// already approved/rejected it).
	ErrBookingNotPending = errors.New("booking is not pending")
	// ErrApprovalConflict is returned when another approved/in_use/finished
	// booking already overlaps the requested window.
	ErrApprovalConflict = errors.New("resource already booked for the requested window")
)

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
	b.id, b.resource_id, b.user_id, b.start_time, b.end_time, b.status, b.purpose, b.admin_notes, b.created_at, b.updated_at
`

func populateDerived(b *models.Booking) {
	b.Date = utils.DateOnlyJakarta(b.StartTime).Format("2006-01-02")
	b.EndDate = utils.DateOnlyJakarta(b.EndTime).Format("2006-01-02")
}

func scanBooking(row pgx.Row) (*models.Booking, error) {
	var b models.Booking
	if err := row.Scan(&b.ID, &b.ResourceID, &b.UserID, &b.StartTime, &b.EndTime, &b.Status, &b.Purpose, &b.AdminNotes, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	populateDerived(&b)
	return &b, nil
}

// FindOverlapping returns bookings on resourceID whose [start,end) window
// overlaps the given range and whose status is in statuses, optionally
// excluding one booking id. Ordered by start_time so callers can treat the
// first result as "the" conflicting booking. Pass a nil tx to run outside a
// transaction.
func (r *BookingRepository) FindOverlapping(ctx context.Context, tx pgx.Tx, resourceID string, start, end time.Time, statuses []string, excludeBookingID string) ([]models.Booking, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM public.bookings b
		WHERE b.resource_id = $1
		  AND b.status = ANY($2)
		  AND b.start_time < $4
		  AND b.end_time > $3
		  AND ($5 = '' OR b.id != $5::uuid)
		ORDER BY b.start_time ASC
	`, bookingColumns)

	var rows pgx.Rows
	var err error
	if tx != nil {
		rows, err = tx.Query(ctx, query, resourceID, statuses, start, end, excludeBookingID)
	} else {
		rows, err = r.pool.Query(ctx, query, resourceID, statuses, start, end, excludeBookingID)
	}
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

// SetStatus performs a plain, unconditional status transition (used for
// reject/cancel, where no auto-reject fan-out or conflict re-check applies).
func (r *BookingRepository) SetStatus(ctx context.Context, id, status string) (*models.Booking, error) {
	return r.SetStatusWithNotes(ctx, id, status, "")
}

// SetStatusWithNotes is like SetStatus but also stamps admin_notes (used for
// revoke, and internally for auto-reject).
func (r *BookingRepository) SetStatusWithNotes(ctx context.Context, id, status, adminNotes string) (*models.Booking, error) {
	query := fmt.Sprintf(`
		UPDATE public.bookings
		SET status = $1, admin_notes = CASE WHEN $2 = '' THEN admin_notes ELSE $2 END
		WHERE id = $3
		RETURNING %s
	`, bookingColumns)
	row := r.pool.QueryRow(ctx, query, status, adminNotes, id)
	b, err := scanBooking(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// ApproveWithAutoReject atomically approves a pending booking, but only if
// no approved/in_use/finished booking already overlaps its window. On
// success, every other *pending* booking overlapping the same window is
// auto-rejected with an explanatory admin_notes entry. Returns the updated
// booking plus the ids of any auto-rejected bookings.
func (r *BookingRepository) ApproveWithAutoReject(ctx context.Context, id string) (*models.Booking, []string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	lockQuery := fmt.Sprintf(`SELECT %s FROM public.bookings b WHERE b.id = $1 FOR UPDATE`, bookingColumns)
	row := tx.QueryRow(ctx, lockQuery, id)
	booking, err := scanBooking(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if booking.Status != models.BookingStatusPending {
		return nil, nil, ErrBookingNotPending
	}

	conflicts, err := r.FindOverlapping(ctx, tx, booking.ResourceID, booking.StartTime, booking.EndTime, models.StatusesBlockingNewBooking, booking.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(conflicts) > 0 {
		return nil, nil, ErrApprovalConflict
	}

	updateQuery := fmt.Sprintf(`
		UPDATE public.bookings SET status = 'approved' WHERE id = $1
		RETURNING %s
	`, bookingColumns)
	row = tx.QueryRow(ctx, updateQuery, id)
	approved, err := scanBooking(row)
	if err != nil {
		return nil, nil, err
	}

	pendingOverlaps, err := r.FindOverlapping(ctx, tx, booking.ResourceID, booking.StartTime, booking.EndTime, []string{models.BookingStatusPending}, booking.ID)
	if err != nil {
		return nil, nil, err
	}

	var autoRejectedIDs []string
	for _, p := range pendingOverlaps {
		_, err := tx.Exec(ctx, `
			UPDATE public.bookings SET status = 'rejected', admin_notes = $1 WHERE id = $2
		`, "Auto-rejected: slot approved for another request", p.ID)
		if err != nil {
			return nil, nil, err
		}
		autoRejectedIDs = append(autoRejectedIDs, p.ID)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}

	return approved, autoRejectedIDs, nil
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
	query := fmt.Sprintf(`SELECT %s FROM public.bookings b WHERE b.status IN ('pending','approved','in_use','finished')`, bookingColumns)
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
