package services

import (
	"context"
	"net/http"
	"strings"
	"time"

	"office-craft-api/internal/config"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
)

type AuthService struct {
	cfg    *config.Config
	users  *repository.UserRepository
	client *http.Client
	notify *NotifyService
}

func NewAuthService(cfg *config.Config, users *repository.UserRepository, notify *NotifyService) *AuthService {
	return &AuthService{
		cfg:    cfg,
		users:  users,
		client: &http.Client{Timeout: 15 * time.Second},
		notify: notify,
	}
}

// AuthError carries the HTTP status + a stable code so handlers can relay a
// consistent { error, message } body.
type AuthError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *AuthError) Error() string { return e.Message }

// Login delegates credential authentication to Central SSO, then resolves
// SILAPET-specific authorization from app_memberships.
func (s *AuthService) Login(
	ctx context.Context,
	email,
	password string,
) (*AuthSession, error) {
	session, err := s.centralLogin(
		ctx,
		strings.TrimSpace(email),
		password,
	)
	if err != nil {
		return nil, err
	}

	result, err := s.buildSilapetSession(ctx, session)
	if err != nil {
		s.discardCentralSession(session)
		return nil, err
	}

	return result, nil
}

// Refresh delegates session rotation to Central SSO and re-checks SILAPET
// membership before returning the rotated session.
func (s *AuthService) Refresh(
	ctx context.Context,
	refreshToken string,
) (*AuthSession, error) {
	refreshToken = strings.TrimSpace(refreshToken)

	if refreshToken == "" {
		return nil, &AuthError{
			StatusCode: http.StatusBadRequest,
			Code:       "VALIDATION_ERROR",
			Message:    "refreshToken is required",
		}
	}

	session, err := s.centralRefresh(ctx, refreshToken)
	if err != nil {
		return nil, err
	}

	result, err := s.buildSilapetSession(ctx, session)
	if err != nil {
		s.discardCentralSession(session)
		return nil, err
	}

	return result, nil
}

// Logout delegates session invalidation to Central SSO.
func (s *AuthService) Logout(
	ctx context.Context,
	accessToken string,
) error {
	accessToken = strings.TrimSpace(accessToken)

	if accessToken == "" {
		return &AuthError{
			StatusCode: http.StatusUnauthorized,
			Code:       "INVALID_TOKEN",
			Message:    "access token is required",
		}
	}

	return s.centralLogout(ctx, accessToken)
}

// discardCentralSession best-effort revokes a Central SSO session that
// SILAPET cannot return to the caller, for example when application
// membership is unavailable after central authentication succeeded.
func (s *AuthService) discardCentralSession(
	session *centralSSOSession,
) {
	if session == nil ||
		strings.TrimSpace(session.AccessToken) == "" {
		return
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		3*time.Second,
	)
	defer cancel()

	_ = s.centralLogout(ctx, session.AccessToken)
}

func (s *AuthService) buildSilapetSession(
	ctx context.Context,
	session *centralSSOSession,
) (*AuthSession, error) {
	if session == nil ||
		strings.TrimSpace(session.AccessToken) == "" ||
		strings.TrimSpace(session.User.ID) == "" {

		return nil, &AuthError{
			StatusCode: http.StatusBadGateway,
			Code:       "INVALID_SSO_RESPONSE",
			Message:    "central authentication returned an invalid session",
		}
	}

	user, err := s.authorizeSilapetUser(
		ctx,
		session.User.ID,
	)
	if err != nil {
		return nil, err
	}

	return &AuthSession{
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		TokenType:    session.TokenType,
		ExpiresIn:    session.ExpiresIn,
		ExpiresAt:    session.ExpiresAt,
		User:         user,
	}, nil
}

func (s *AuthService) authorizeSilapetUser(
	ctx context.Context,
	userID string,
) (*models.AppUser, error) {
	user, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if user == nil {
		return nil, &AuthError{
			StatusCode: http.StatusForbidden,
			Code:       "APPLICATION_ACCESS_UNAVAILABLE",
			Message:    "your account does not have access to SILAPET",
		}
	}

	switch user.Status {
	case models.UserStatusApproved:
		return user, nil

	case models.UserStatusPending:
		return nil, &AuthError{
			StatusCode: http.StatusForbidden,
			Code:       "ACCOUNT_PENDING_APPROVAL",
			Message:    "your account is awaiting admin approval",
		}

	case models.UserStatusRejected:
		return nil, &AuthError{
			StatusCode: http.StatusForbidden,
			Code:       "ACCOUNT_REJECTED",
			Message:    "your account access request was rejected",
		}

	case models.UserStatusDisabled:
		return nil, &AuthError{
			StatusCode: http.StatusForbidden,
			Code:       "ACCOUNT_DISABLED",
			Message:    "your SILAPET access is disabled",
		}

	default:
		return nil, &AuthError{
			StatusCode: http.StatusForbidden,
			Code:       "APPLICATION_ACCESS_UNAVAILABLE",
			Message:    "your account does not have active access to SILAPET",
		}
	}
}

// Register creates a Central Balai identity through Central SSO, then creates
// only the SILAPET membership. SILAPET never owns or stores credentials here.
func (s *AuthService) Register(
	ctx context.Context,
	email string,
	password string,
	fullName string,
	phone string,
) (*models.AppUser, error) {
	centralUser, err := s.centralRegister(
		ctx,
		email,
		password,
		fullName,
		phone,
	)
	if err != nil {
		return nil, err
	}

	user, created, err := s.users.CreateMembership(
		ctx,
		centralUser.ID,
		models.RoleUser,
		models.UserStatusPending,
	)
	if err != nil {
		return nil, err
	}

	if created {
		s.fireAdminNotify(*user, "registered", "")
		go s.notify.NotifyRegistrationReceived(*user)
	}

	return user, nil
}

type AccessRequestResult struct {
	User    *models.AppUser
	Created bool
}

// RequestAccess verifies an existing Akun Balai through Central SSO and then
// creates only a pending SILAPET membership.
//
// The temporary Central SSO session is discarded because this endpoint proves
// identity ownership; it is not a SILAPET login.
func (s *AuthService) RequestAccess(
	ctx context.Context,
	email string,
	password string,
) (*AccessRequestResult, error) {
	session, err := s.centralLogin(
		ctx,
		strings.TrimSpace(email),
		password,
	)
	if err != nil {
		return nil, err
	}

	defer s.discardCentralSession(session)

	if session == nil ||
		strings.TrimSpace(session.User.ID) == "" {

		return nil, &AuthError{
			StatusCode: http.StatusBadGateway,
			Code:       "INVALID_SSO_RESPONSE",
			Message:    "central authentication returned an invalid identity",
		}
	}

	user, created, err := s.users.CreateMembership(
		ctx,
		session.User.ID,
		models.RoleUser,
		models.UserStatusPending,
	)
	if err != nil {
		return nil, err
	}

	if !created {
		switch user.Status {
		case models.UserStatusApproved:
			return nil, &AuthError{
				StatusCode: http.StatusConflict,
				Code:       "APPLICATION_ACCESS_EXISTS",
				Message:    "your account already has access to SILAPET",
			}

		case models.UserStatusRejected:
			return nil, &AuthError{
				StatusCode: http.StatusConflict,
				Code:       "APPLICATION_ACCESS_REJECTED",
				Message:    "your previous SILAPET access request was rejected",
			}

		case models.UserStatusDisabled:
			return nil, &AuthError{
				StatusCode: http.StatusConflict,
				Code:       "APPLICATION_ACCESS_DISABLED",
				Message:    "your SILAPET access is disabled",
			}
		}
	}

	if created {
		s.fireAdminNotify(*user, "registered", "")
		go s.notify.NotifyRegistrationReceived(*user)
	}

	return &AccessRequestResult{
		User:    user,
		Created: created,
	}, nil
}

// ChangePassword delegates Akun Balai credential ownership to Central SSO.
func (s *AuthService) ChangePassword(
	ctx context.Context,
	accessToken string,
	oldPassword string,
	newPassword string,
) error {
	return s.centralChangePassword(
		ctx,
		accessToken,
		oldPassword,
		newPassword,
	)
}

// ForgotPassword delegates recovery initiation to Central SSO.
// Central SSO performs SILAPET membership eligibility checks and preserves
// generic public responses to prevent account enumeration.
func (s *AuthService) ForgotPassword(
	ctx context.Context,
	email string,
) error {
	return s.centralForgotPassword(
		ctx,
		strings.TrimSpace(email),
	)
}

// ResetPassword delegates recovery verification and password update to Central SSO.
func (s *AuthService) ResetPassword(
	ctx context.Context,
	tokenHash string,
	newPassword string,
) error {
	return s.centralResetPassword(
		ctx,
		tokenHash,
		newPassword,
	)
}

// ChangePhoneNumber updates partial the user's phone number in app_users table
func (s *AuthService) ChangePhoneNumber(ctx context.Context, user *models.AppUser, phone string) error {
	if strings.HasPrefix(phone, "0") {
		phone = "62" + phone[1:]
	}

	_, err := s.users.UpdatePhone(ctx, user.ID, phone)
	return err
}

// fireAdminNotify is fireNotify's counterpart for the admin-facing
// direction (new request / started / finished) - same background-goroutine
// rationale, different recipient set.
func (s *AuthService) fireAdminNotify(user models.AppUser, kind, note string) {
	go s.notify.NotifyAdminsForUserEvent(context.Background(), user, kind, note)
}
