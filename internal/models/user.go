package models

import "time"

// AppUser mirrors the public.app_users table. JSON tags are camelCase to
// match the TanStack Start frontend's TypeScript interfaces.
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

const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// New registrations start "pending" and need an admin to approve them
// before they can log in or use any authenticated endpoint. "rejected" is a
// terminal state an admin can also set.
const (
	UserStatusPending  = "pending"
	UserStatusApproved = "approved"
	UserStatusRejected = "rejected"
)
