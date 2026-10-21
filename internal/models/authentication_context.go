package models

// AuthenticationContext is a read-only projection used to validate a
// Supabase session, Central SSO identity mapping, and application membership.
type AuthenticationContext struct {
	AppUserID         string
	AuthUserID        string
	Email             string
	UserDeleted       bool
	SessionFound      bool
	SessionUserID     string
	ApplicationFound  bool
	ApplicationCode   string
	ApplicationActive bool
	MembershipFound   bool
	MembershipRole    string
	MembershipStatus  string
	MembershipDeleted bool
}
