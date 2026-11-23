package services

import (
	"context"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
	"office-craft-api/internal/utils"
)

type proofBookingFinder interface {
	GetByID(ctx context.Context, id string) (*models.Booking, error)
}

type ProofService struct {
	bookings proofBookingFinder
}

func NewProofService(bookings proofBookingFinder) *ProofService {
	return &ProofService{bookings: bookings}
}

// ValidateUpload checks the "kind" gating rules:
//   - "before": booking status ∈ {approved, in_use} AND today (Asia/Jakarta)
//     is on or after the start date.
//   - "after": booking status ∈ {in_use, needs_revision}, with no date limit,
//     so staff can complete a booking after its end date.
//
// Returns the booking (so the caller doesn't need to re-fetch it) or an
// apperror.AppError describing why the upload isn't allowed.
func (s *ProofService) ValidateUpload(ctx context.Context, bookingID, kind string) (*models.Booking, error) {
	booking, err := s.bookings.GetByID(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if booking == nil {
		return nil, apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}

	switch kind {
	case models.ProofKindBefore:
		statusOK := booking.Status == models.BookingStatusApproved || booking.Status == models.BookingStatusInUse
		if !statusOK || utils.TodayJakarta().Before(utils.DateOnlyJakarta(booking.StartTime)) {
			return nil, apperror.Forbidden("PROOF_NOT_ALLOWED", "before-photos are only allowed while the booking is approved/in_use, from its start date")
		}
	case models.ProofKindAfter:
		if booking.Status != models.BookingStatusInUse && booking.Status != models.BookingStatusNeedsRevision {
			return nil, apperror.Forbidden("PROOF_NOT_ALLOWED", "after-photos are only allowed while the booking is in_use or needs_revision")
		}
	default:
		return nil, apperror.BadRequest("INVALID_KIND", "kind must be 'before' or 'after'")
	}

	return booking, nil
}
