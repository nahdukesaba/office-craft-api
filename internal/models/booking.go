package models

import "time"

// Booking lifecycle: pending -> approved -> in_use -> finished, with
// rejected/cancelled as terminal off-ramps from pending/approved/in_use.
// From "finished", an admin either closes the booking (final sign-off,
// after reviewing the before/after proof photos) or requests a revision if
// the proofs are insufficient - which sends it to "needs_revision" until
// the user uploads better after-photos and calls /finish again.
const (
	BookingStatusPending       = "pending"
	BookingStatusApproved      = "approved"
	BookingStatusInUse         = "in_use"
	BookingStatusFinished      = "finished"
	BookingStatusNeedsRevision = "needs_revision"
	BookingStatusClosed        = "closed"
	BookingStatusRejected      = "rejected"
	BookingStatusCancelled     = "cancelled"
)

// StatusesBlockingNewBooking are the statuses that make a resource
// unavailable for a new overlapping booking. A "pending" booking no longer
// blocks other pending requests - only a slot that is actually spoken for
// (approved/in_use) or was already used (finished/needs_revision)
// does. Closed status no longer blocks other pending requests, since
// the user and admin already completed all lifecycle, and resource
// are ready to be booked again
var StatusesBlockingNewBooking = []string{
	BookingStatusApproved, BookingStatusInUse, BookingStatusFinished,
	BookingStatusNeedsRevision,
}

// Booking mirrors public.bookings. Date/EndDate are derived (Asia/Jakarta
// calendar date of StartTime/EndTime) for display convenience - they are
// not stored columns.
type Booking struct {
	ID         string    `json:"id" db:"id"`
	ResourceID string    `json:"resourceId" db:"resource_id"`
	UserID     string    `json:"userId" db:"user_id"`
	StartTime  time.Time `json:"startTime" db:"start_time"`
	EndTime    time.Time `json:"endTime" db:"end_time"`
	Date       string    `json:"date"`
	EndDate    string    `json:"endDate"`
	Status     string    `json:"status" db:"status"`
	Purpose    string    `json:"purpose" db:"purpose"`
	AdminNotes string    `json:"adminNotes" db:"admin_notes"`
	CreatedAt  time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt  time.Time `json:"updatedAt" db:"updated_at"`
}

// BookingWithDetails is returned by GET /bookings/:id (and can be reused
// for list endpoints) with nested resource + user objects.
type BookingWithDetails struct {
	Booking
	Resource *Resource `json:"resource,omitempty"`
	User     *AppUser  `json:"user,omitempty"`
}

// BookingInput is the shape accepted on POST /bookings.
// userId is intentionally absent: it is always derived from the JWT.
type BookingInput struct {
	ResourceID string    `json:"resourceId"`
	StartTime  time.Time `json:"startTime"`
	EndTime    time.Time `json:"endTime"`
	Purpose    string    `json:"purpose"`
}

// RejectInput is the shape accepted on PUT /bookings/:id/reject.
type RejectInput struct {
	AdminNotes string `json:"adminNotes"`
}

// RevokeInput is the shape accepted on PUT /bookings/:id/revoke.
type RevokeInput struct {
	AdminNotes string `json:"adminNotes"`
	Reason     string `json:"reason"`
}

// CloseInput is the shape accepted on PUT /bookings/:id/close.
type CloseInput struct {
	AdminNotes string `json:"adminNotes"`
}

// RequestRevisionInput is the shape accepted on PUT /bookings/:id/request-revision.
type RequestRevisionInput struct {
	AdminNotes string `json:"adminNotes"`
}

// ApproveBookingResponse is returned by PUT /bookings/:id/approve.
type ApproveBookingResponse struct {
	Booking         BookingWithDetails `json:"booking"`
	AutoRejectedIDs []string           `json:"autoRejectedIds"`
}

// ConflictDetail describes the booking a create/approve attempt collided with.
type ConflictDetail struct {
	ID           string `json:"id"`
	UserFullName string `json:"userFullName"`
	StartTime    string `json:"startTime"`
	EndTime      string `json:"endTime"`
	Date         string `json:"date"`
	EndDate      string `json:"endDate"`
}

// PaginatedBookings is the envelope returned by GET /bookings.
type PaginatedBookings struct {
	Data       []BookingWithDetails `json:"data"`
	Page       int                  `json:"page"`
	PageSize   int                  `json:"pageSize"`
	Total      int64                `json:"total"`
	TotalPages int                  `json:"totalPages"`
}
