package models

import "time"

// Event types recorded in booking_events. One row per status transition.
const (
	EventCreated           = "created"
	EventApproved          = "approved"
	EventAutoRejected      = "auto_rejected"
	EventRejected          = "rejected"
	EventStarted           = "started"
	EventFinished          = "finished"
	EventCancelled         = "cancelled"
	EventRevoked           = "revoked"
	EventClosed            = "closed"
	EventRevisionRequested = "revision_requested"
)

// BookingEvent mirrors public.booking_events - one row per status change.
type BookingEvent struct {
	ID         string    `json:"id" db:"id"`
	BookingID  string    `json:"bookingId" db:"booking_id"`
	EventType  string    `json:"eventType" db:"event_type"`
	FromStatus *string   `json:"fromStatus" db:"from_status"`
	ToStatus   string    `json:"toStatus" db:"to_status"`
	ActorID    *string   `json:"actorId" db:"actor_id"`
	Notes      string    `json:"notes" db:"notes"`
	CreatedAt  time.Time `json:"createdAt" db:"created_at"`
}

// TimelineEntryType distinguishes the two kinds of entries merged into a
// booking's timeline.
const (
	TimelineStatusChange  = "status_change"
	TimelineProofUploaded = "proof_uploaded"
)

// TimelineEntry is a single row in a booking's merged, chronological
// timeline - either a status change (from booking_events) or a proof
// upload (from booking_proofs). Fields irrelevant to the entry's Type are
// omitted from the JSON output.
type TimelineEntry struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	ActorID   *string   `json:"actorId,omitempty"`
	Actor     *AppUser  `json:"actor,omitempty"`

	// status_change fields
	EventType  string  `json:"eventType,omitempty"`
	FromStatus *string `json:"fromStatus,omitempty"`
	ToStatus   string  `json:"toStatus,omitempty"`
	Notes      string  `json:"notes,omitempty"`

	// proof_uploaded fields
	ProofID   string `json:"proofId,omitempty"`
	ProofKind string `json:"proofKind,omitempty"`
	ProofPath string `json:"proofPath,omitempty"`
}
