package models

import "time"

// AppUser mirrors the public.app_users table. JSON tags are camelCase to
// match the TanStack Start frontend's TypeScript interfaces.
type AppUser struct {
	ID        string    `json:"id" db:"id"`
	Email     string    `json:"email" db:"email"`
	FullName  string    `json:"fullName" db:"full_name"`
	Role      string    `json:"role" db:"role"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt time.Time `json:"updatedAt" db:"updated_at"`
}

const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)
