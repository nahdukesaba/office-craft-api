package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/models"
)

type BookingEventRepository struct {
	pool *pgxpool.Pool
}

func NewBookingEventRepository(pool *pgxpool.Pool) *BookingEventRepository {
	return &BookingEventRepository{pool: pool}
}

// Create records one status-transition event. actorID may be empty when
// there's no authenticated actor to attribute it to (shouldn't normally
// happen given every mutating endpoint requires auth, but kept nil-safe).
func (r *BookingEventRepository) Create(ctx context.Context, bookingID, eventType string, fromStatus *string, toStatus, actorID, notes string) error {
	var actorParam interface{}
	if actorID != "" {
		actorParam = actorID
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.booking_events (booking_id, event_type, from_status, to_status, actor_id, notes)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, bookingID, eventType, fromStatus, toStatus, actorParam, notes)
	return err
}

func (r *BookingEventRepository) ListByBooking(ctx context.Context, bookingID string) ([]models.BookingEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, booking_id, event_type, from_status, to_status, actor_id, notes, created_at
		FROM public.booking_events
		WHERE booking_id = $1 AND deleted_at IS NULL
		ORDER BY created_at ASC
	`, bookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.BookingEvent
	for rows.Next() {
		var e models.BookingEvent
		if err := rows.Scan(&e.ID, &e.BookingID, &e.EventType, &e.FromStatus, &e.ToStatus, &e.ActorID, &e.Notes, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
