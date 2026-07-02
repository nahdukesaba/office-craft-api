package models

import "time"

const (
	ProofKindBefore = "before"
	ProofKindAfter  = "after"
)

// BookingProof mirrors public.booking_proofs.
type BookingProof struct {
	ID         string    `json:"id" db:"id"`
	BookingID  string    `json:"bookingId" db:"booking_id"`
	Kind       string    `json:"kind" db:"kind"`
	Path       string    `json:"path" db:"path"`
	UploadedBy string    `json:"uploadedBy" db:"uploaded_by"`
	CreatedAt  time.Time `json:"createdAt" db:"created_at"`
}

// ProofInput is the shape accepted on POST /bookings/:bookingId/proofs.
type ProofInput struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}
