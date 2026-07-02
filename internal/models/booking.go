package models

import "time"

const (
	BookingStatusPending   = "pending"
	BookingStatusApproved  = "approved"
	BookingStatusRejected  = "rejected"
	BookingStatusCompleted = "completed"
	BookingStatusCancelled = "cancelled"
)

// Booking mirrors public.bookings.
type Booking struct {
	ID         string    `json:"id" db:"id"`
	ResourceID string    `json:"resourceId" db:"resource_id"`
	UserID     string    `json:"userId" db:"user_id"`
	StartTime  time.Time `json:"startTime" db:"start_time"`
	EndTime    time.Time `json:"endTime" db:"end_time"`
	Status     string    `json:"status" db:"status"`
	Purpose    string    `json:"purpose" db:"purpose"`
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

// PaginatedBookings is the envelope returned by GET /bookings.
type PaginatedBookings struct {
	Data       []BookingWithDetails `json:"data"`
	Page       int                  `json:"page"`
	PageSize   int                  `json:"pageSize"`
	Total      int64                `json:"total"`
	TotalPages int                  `json:"totalPages"`
}
