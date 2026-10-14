package models

import "time"

// AppUser is the SILAPET-facing user representation.
//
// Identity/profile fields come from public.app_users.
// Role and Status come from the user's SILAPET app_membership.
//
// The JSON shape intentionally remains unchanged so the existing frontend
// does not need to change during the authorization migration.
type AppUser struct {
	ID        string    `json:"id" db:"id"`
	Email     string    `json:"email" db:"email"`
	FullName  string    `json:"fullName" db:"full_name"`
	Phone     *string   `json:"phone" db:"phone"`
	Role      string    `json:"role" db:"role"`
	Status    string    `json:"status" db:"status"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt time.Time `json:"updatedAt" db:"updated_at"`
}

const ApplicationCodeSilapet = "silapet"

const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// These values now describe the SILAPET membership lifecycle rather than
// the legacy app_users.status column.
const (
	UserStatusPending  = "pending"
	UserStatusApproved = "approved"
	UserStatusRejected = "rejected"
	UserStatusDisabled = "disabled"
)
