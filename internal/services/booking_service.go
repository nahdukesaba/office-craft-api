package services

import (
	"context"
	"errors"
	"time"

	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
)

var (
	ErrInvalidInterval = errors.New("booking times must fall on 30-minute intervals")
	ErrTooLong         = errors.New("bookings cannot exceed 4 hours")
	ErrPastBooking     = errors.New("booking start time must be in the future")
	ErrEndBeforeStart  = errors.New("end time must be after start time")
	ErrResourceMissing = errors.New("resource not found")
	ErrResourceOffline = errors.New("resource is not available for booking")
	ErrConflict        = errors.New("resource is already booked for the requested time range")
)

const (
	maxBookingDuration = 4 * time.Hour
	intervalStep       = 30 * time.Minute
)

type BookingService struct {
	bookings  *repository.BookingRepository
	resources *repository.ResourceRepository
}

func NewBookingService(bookings *repository.BookingRepository, resources *repository.ResourceRepository) *BookingService {
	return &BookingService{bookings: bookings, resources: resources}
}

func isOnInterval(t time.Time) bool {
	return t.Minute()%30 == 0 && t.Second() == 0 && t.Nanosecond() == 0
}

// Validate applies the business rules shared by create (and could be reused
// by an admin "reschedule" flow in the future).
func (s *BookingService) Validate(ctx context.Context, in models.BookingInput) error {
	if !in.EndTime.After(in.StartTime) {
		return ErrEndBeforeStart
	}
	if !isOnInterval(in.StartTime) || !isOnInterval(in.EndTime) {
		return ErrInvalidInterval
	}
	if in.EndTime.Sub(in.StartTime) > maxBookingDuration {
		return ErrTooLong
	}
	if in.StartTime.Before(time.Now()) {
		return ErrPastBooking
	}

	resource, err := s.resources.GetByID(ctx, in.ResourceID)
	if err != nil {
		return err
	}
	if resource == nil {
		return ErrResourceMissing
	}
	if !resource.IsAvailable {
		return ErrResourceOffline
	}

	conflict, err := s.bookings.HasConflict(ctx, in.ResourceID, in.StartTime, in.EndTime, "")
	if err != nil {
		return err
	}
	if conflict {
		return ErrConflict
	}

	return nil
}

func (s *BookingService) Create(ctx context.Context, userID string, in models.BookingInput) (*models.Booking, error) {
	if err := s.Validate(ctx, in); err != nil {
		return nil, err
	}
	return s.bookings.Create(ctx, userID, in)
}
