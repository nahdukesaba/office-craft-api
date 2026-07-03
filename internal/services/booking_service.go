package services

import (
	"context"
	"time"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/utils"
)

const (
	maxBookingDuration = 4 * time.Hour
	intervalStep       = 30 * time.Minute
)

type BookingService struct {
	bookings  *repository.BookingRepository
	resources *repository.ResourceRepository
	users     *repository.UserRepository
	proofs    *repository.ProofRepository
	events    *repository.BookingEventRepository
}

func NewBookingService(bookings *repository.BookingRepository, resources *repository.ResourceRepository, users *repository.UserRepository, proofs *repository.ProofRepository, events *repository.BookingEventRepository) *BookingService {
	return &BookingService{bookings: bookings, resources: resources, users: users, proofs: proofs, events: events}
}

func isOnInterval(t time.Time) bool {
	return t.Minute()%30 == 0 && t.Second() == 0 && t.Nanosecond() == 0
}

// recordEvent writes to the audit trail and logs (but does not fail the
// request on) any write error - losing an audit-log write is not a reason
// to roll back or fail a booking action that already succeeded.
func (s *BookingService) recordEvent(ctx context.Context, bookingID, eventType string, fromStatus *string, toStatus, actorID, notes string) {
	_ = s.events.Create(ctx, bookingID, eventType, fromStatus, toStatus, actorID, notes)
}

// buildConflictDetail loads the booking owner's name to populate the
// conflictWith payload the frontend uses for its toast.
func (s *BookingService) buildConflictDetail(ctx context.Context, b models.Booking) models.ConflictDetail {
	fullName := ""
	if u, err := s.users.GetByID(ctx, b.UserID); err == nil && u != nil {
		fullName = u.FullName
	}
	return models.ConflictDetail{
		ID:           b.ID,
		UserFullName: fullName,
		StartTime:    b.StartTime.Format(time.RFC3339),
		EndTime:      b.EndTime.Format(time.RFC3339),
		Date:         b.Date,
		EndDate:      b.EndDate,
	}
}

// Validate applies the business rules for a new booking request. A booking
// is only blocked by an overlapping approved/in_use/finished booking -
// other pending requests may coexist (they get resolved at approval time).
func (s *BookingService) Validate(ctx context.Context, in models.BookingInput) error {
	if !in.EndTime.After(in.StartTime) {
		return apperror.BadRequest("INVALID_RANGE", "end time must be after start time")
	}
	if !isOnInterval(in.StartTime) || !isOnInterval(in.EndTime) {
		return apperror.BadRequest("INVALID_INTERVAL", "booking times must fall on 30-minute intervals")
	}
	if in.EndTime.Sub(in.StartTime) > maxBookingDuration {
		return apperror.BadRequest("TOO_LONG", "bookings cannot exceed 4 hours")
	}
	if in.StartTime.Before(time.Now()) {
		return apperror.BadRequest("PAST_BOOKING", "booking start time must be in the future")
	}

	resource, err := s.resources.GetByID(ctx, in.ResourceID)
	if err != nil {
		return err
	}
	if resource == nil {
		return apperror.NotFound("RESOURCE_NOT_FOUND", "resource not found")
	}
	if !resource.IsAvailable {
		return apperror.Conflict("RESOURCE_UNAVAILABLE", "resource is not available for booking")
	}

	conflicts, err := s.bookings.FindOverlapping(ctx, nil, in.ResourceID, in.StartTime, in.EndTime, models.StatusesBlockingNewBooking, "")
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		return apperror.WithDetails(409, "BOOKING_CONFLICT", "Slot already approved for another user", s.buildConflictDetail(ctx, conflicts[0]))
	}

	return nil
}

func (s *BookingService) Create(ctx context.Context, userID string, in models.BookingInput) (*models.Booking, error) {
	if err := s.Validate(ctx, in); err != nil {
		return nil, err
	}
	booking, err := s.bookings.Create(ctx, userID, in)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, booking.ID, models.EventCreated, nil, models.BookingStatusPending, userID, "")
	return booking, nil
}

// Approve approves a pending booking and auto-rejects any other pending
// booking that overlaps the same window. Returns the approved booking and
// the ids of anything auto-rejected.
func (s *BookingService) Approve(ctx context.Context, actorID, id string) (*models.Booking, []string, error) {
	booking, err := s.bookings.GetByID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if booking == nil {
		return nil, nil, apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}

	approved, autoRejectedIDs, err := s.bookings.ApproveWithAutoReject(ctx, id)
	if err != nil {
		switch err {
		case repository.ErrBookingNotPending:
			return nil, nil, apperror.Conflict("NOT_PENDING", "booking is not pending")
		case repository.ErrApprovalConflict:
			conflicts, cErr := s.bookings.FindOverlapping(ctx, nil, booking.ResourceID, booking.StartTime, booking.EndTime, models.StatusesBlockingNewBooking, booking.ID)
			if cErr == nil && len(conflicts) > 0 {
				return nil, nil, apperror.WithDetails(409, "BOOKING_CONFLICT", "Slot already approved for another user", s.buildConflictDetail(ctx, conflicts[0]))
			}
			return nil, nil, apperror.Conflict("BOOKING_CONFLICT", "Slot already approved for another user")
		default:
			return nil, nil, err
		}
	}
	if approved == nil {
		return nil, nil, apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}

	s.recordEvent(ctx, approved.ID, models.EventApproved, strPtr(models.BookingStatusPending), models.BookingStatusApproved, actorID, "")
	for _, rejectedID := range autoRejectedIDs {
		s.recordEvent(ctx, rejectedID, models.EventAutoRejected, strPtr(models.BookingStatusPending), models.BookingStatusRejected, actorID, "Auto-rejected: slot approved for another request")
	}

	return approved, autoRejectedIDs, nil
}

// Reject transitions a pending booking to rejected.
func (s *BookingService) Reject(ctx context.Context, actorID, id string) (*models.Booking, error) {
	return s.transition(ctx, actorID, id, []string{models.BookingStatusPending}, models.BookingStatusRejected, models.EventRejected, "")
}

// Cancel transitions a pending or approved booking to cancelled.
func (s *BookingService) Cancel(ctx context.Context, actorID, id string) (*models.Booking, error) {
	return s.transition(ctx, actorID, id, []string{models.BookingStatusPending, models.BookingStatusApproved}, models.BookingStatusCancelled, models.EventCancelled, "")
}

// Revoke transitions an approved/in_use booking to cancelled, as an admin
// override, stamping admin_notes with the supplied reason.
func (s *BookingService) Revoke(ctx context.Context, actorID, id, adminNotes, reason string) (*models.Booking, error) {
	notes := "Revoked by admin"
	if adminNotes != "" {
		notes = "Revoked by admin: " + adminNotes
	} else if reason != "" {
		notes = "Revoked by admin: " + reason
	}
	return s.transition(ctx, actorID, id, []string{models.BookingStatusApproved, models.BookingStatusInUse}, models.BookingStatusCancelled, models.EventRevoked, notes)
}

// Start transitions an approved booking to in_use. Only allowed if today
// (Asia/Jakarta) falls within [date, endDate].
func (s *BookingService) Start(ctx context.Context, actorID, id string) (*models.Booking, error) {
	booking, err := s.bookings.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if booking == nil {
		return nil, apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if booking.Status != models.BookingStatusApproved {
		return nil, apperror.Conflict("INVALID_STATUS", "booking must be approved before it can be started")
	}
	if !utils.WithinInclusiveDateRange(utils.TodayJakarta(), booking.StartTime, booking.EndTime) {
		return nil, apperror.Forbidden("NOT_START_DAY", "today is not within the booking window")
	}

	// Mirrors the frontend's gate, enforced server-side too: a booking
	// can't move to in_use without a "before" proof already on file, so
	// hitting the API directly can't skip the photo step and leave the
	// audit trail inconsistent.
	hasBefore, err := s.hasProof(ctx, id, models.ProofKindBefore)
	if err != nil {
		return nil, err
	}
	if !hasBefore {
		return nil, apperror.BadRequest("PHOTO_REQUIRED", "a 'before' proof photo is required before starting")
	}

	updated, err := s.bookings.SetStatus(ctx, id, models.BookingStatusInUse)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, id, models.EventStarted, strPtr(models.BookingStatusApproved), models.BookingStatusInUse, actorID, "")
	return updated, nil
}

// hasProof reports whether booking id already has a recorded proof of the
// given kind ("before" or "after").
func (s *BookingService) hasProof(ctx context.Context, bookingID, kind string) (bool, error) {
	proofs, err := s.proofs.ListByBooking(ctx, bookingID)
	if err != nil {
		return false, err
	}
	for _, p := range proofs {
		if p.Kind == kind {
			return true, nil
		}
	}
	return false, nil
}

// Finish transitions an in_use booking to finished. Requires at least one
// "after" proof photo to already be recorded.
func (s *BookingService) Finish(ctx context.Context, actorID, id string) (*models.Booking, error) {
	booking, err := s.bookings.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if booking == nil {
		return nil, apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if booking.Status != models.BookingStatusInUse {
		return nil, apperror.Conflict("INVALID_STATUS", "booking must be in_use before it can be finished")
	}

	hasAfter, err := s.hasProof(ctx, id, models.ProofKindAfter)
	if err != nil {
		return nil, err
	}
	if !hasAfter {
		return nil, apperror.BadRequest("PHOTO_REQUIRED", "an 'after' proof photo is required before finishing")
	}

	updated, err := s.bookings.SetStatus(ctx, id, models.BookingStatusFinished)
	if err != nil {
		return nil, err
	}
	s.recordEvent(ctx, id, models.EventFinished, strPtr(models.BookingStatusInUse), models.BookingStatusFinished, actorID, "")
	return updated, nil
}

// transition is a small shared helper for the simple, non-transactional
// status changes (reject/cancel/revoke) that only need a "current status
// must be one of X" precondition. It also writes the matching audit event.
func (s *BookingService) transition(ctx context.Context, actorID, id string, allowedFrom []string, newStatus, eventType, adminNotes string) (*models.Booking, error) {
	booking, err := s.bookings.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if booking == nil {
		return nil, apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}

	allowed := false
	for _, st := range allowedFrom {
		if booking.Status == st {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, apperror.Conflict("INVALID_STATUS", "booking status "+booking.Status+" cannot transition to "+newStatus)
	}

	updated, err := s.bookings.SetStatusWithNotes(ctx, id, newStatus, adminNotes)
	if err != nil {
		return nil, err
	}
	fromStatus := booking.Status
	s.recordEvent(ctx, id, eventType, &fromStatus, newStatus, actorID, adminNotes)
	return updated, nil
}
