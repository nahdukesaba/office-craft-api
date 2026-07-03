package services

import (
	"context"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/utils"
)

type ProofService struct {
	bookings *repository.BookingRepository
}

func NewProofService(bookings *repository.BookingRepository) *ProofService {
	return &ProofService{bookings: bookings}
}

// ValidateUpload checks the "kind" gating rules from the spec:
//   - "before": booking status ∈ {approved, in_use} AND today (Asia/Jakarta)
//     is between date and endDate inclusive.
//   - "after": booking status = in_use AND today <= endDate.
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

	today := utils.TodayJakarta()

	switch kind {
	case models.ProofKindBefore:
		statusOK := booking.Status == models.BookingStatusApproved || booking.Status == models.BookingStatusInUse
		if !statusOK || !utils.WithinInclusiveDateRange(today, booking.StartTime, booking.EndTime) {
			return nil, apperror.Forbidden("PROOF_NOT_ALLOWED", "before-photos are only allowed while the booking is approved/in_use and today is within the booking window")
		}
	case models.ProofKindAfter:
		statusOK := booking.Status == models.BookingStatusInUse
		endDate := utils.DateOnlyJakarta(booking.EndTime)
		if !statusOK || today.After(endDate) {
			return nil, apperror.Forbidden("PROOF_NOT_ALLOWED", "after-photos are only allowed while the booking is in_use and on or before its end date")
		}
	default:
		return nil, apperror.BadRequest("INVALID_KIND", "kind must be 'before' or 'after'")
	}

	return booking, nil
}
