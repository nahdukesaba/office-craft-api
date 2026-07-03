package services

import (
	"context"
	"log"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
)

// Notifier is the seam for actually delivering a notification (email, push,
// webhook, whatever you wire up later). LogNotifier below is a placeholder
// that just logs - swap it out in router.Setup once you have a real
// provider without touching NotifyService or the handler.
type Notifier interface {
	Notify(ctx context.Context, booking models.Booking, recipientEmail string) error
}

// LogNotifier is a no-op Notifier that just logs what it would have sent.
// Good enough to unblock frontend integration before email/SMS is wired up.
type LogNotifier struct{}

func (LogNotifier) Notify(_ context.Context, booking models.Booking, recipientEmail string) error {
	log.Printf("notify: would notify %s about booking %s (status=%s, %s -> %s)",
		recipientEmail, booking.ID, booking.Status, booking.StartTime, booking.EndTime)
	return nil
}

// notifiableStatuses are the only statuses a notify request is allowed for.
var notifiableStatuses = map[string]bool{
	models.BookingStatusApproved: true,
	models.BookingStatusInUse:    true,
	models.BookingStatusFinished: true,
}

type NotifyService struct {
	bookings *repository.BookingRepository
	users    *repository.UserRepository
	notifier Notifier
}

func NewNotifyService(bookings *repository.BookingRepository, users *repository.UserRepository, notifier Notifier) *NotifyService {
	if notifier == nil {
		notifier = LogNotifier{}
	}
	return &NotifyService{bookings: bookings, users: users, notifier: notifier}
}

// Notify sends a notification about a booking to its owner, but only for
// bookings that are approved/in_use/finished.
func (s *NotifyService) Notify(ctx context.Context, bookingID string) (*models.Booking, error) {
	booking, err := s.bookings.GetByID(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if booking == nil {
		return nil, apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if !notifiableStatuses[booking.Status] {
		return nil, apperror.BadRequest("NOTIFY_NOT_ALLOWED", "notifications are only allowed for approved, in_use, or finished bookings")
	}

	owner, err := s.users.GetByID(ctx, booking.UserID)
	if err != nil {
		return nil, err
	}
	email := ""
	if owner != nil {
		email = owner.Email
	}

	if err := s.notifier.Notify(ctx, *booking, email); err != nil {
		return nil, apperror.Internal("failed to send notification")
	}

	return booking, nil
}
