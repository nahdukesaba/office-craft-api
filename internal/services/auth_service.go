package services

import (
	"context"
	"net/http"
	"strings"
	"time"

	"office-craft-api/internal/config"
	"office-craft-api/internal/models"
)

type AuthUserStore interface {
	GetByID(
		ctx context.Context,
		id string,
	) (*models.AppUser, error)
	GetByEmail(
		ctx context.Context,
		email string,
	) (*models.AppUser, error)
	UpdatePhone(
		ctx context.Context,
		id string,
		phone string,
	) (*models.AppUser, error)
}

type AuthNotifier interface {
	NotifyAdminsForUserEvent(
		ctx context.Context,
		user models.AppUser,
		kind string,
		note string,
	)
	NotifyRegistrationReceived(user models.AppUser)
}

type AuthService struct {
	cfg      *config.Config
	users    AuthUserStore
	client   *http.Client
	notify   AuthNotifier
	ssoFlows BrowserSSOFlowStore
}

func NewAuthService(
	cfg *config.Config,
	users AuthUserStore,
	notify AuthNotifier,
) *AuthService {
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

type LogoutResult struct {
	CentralLogoutRequired bool
}

// Logout delegates application-session invalidation to Central SSO and tells
// the caller whether the verified session originated from Browser SSO.
func (s *AuthService) Logout(
	ctx context.Context,
	accessToken string,
	loginSource models.LoginSource,
) (*LogoutResult, error) {
	accessToken = strings.TrimSpace(accessToken)

	if accessToken == "" {
		return nil, &AuthError{
			StatusCode: http.StatusUnauthorized,
			Code:       "INVALID_TOKEN",
			Message:    "access token is required",
		}
	}

	if err := s.centralLogout(ctx, accessToken); err != nil {
		return nil, err
	}
	return &LogoutResult{
		CentralLogoutRequired: loginSource == models.LoginSourceSSO,
	}, nil
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

// Register delegates identity and initial SILAPET membership creation to
// Central SSO. SILAPET only reloads the resulting projection and dispatches
// its business notifications; it never creates credentials or membership.
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

	user, err := s.users.GetByID(ctx, centralUser.ID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, &AuthError{
			StatusCode: http.StatusBadGateway,
			Code:       "INVALID_SSO_RESPONSE",
			Message:    "central registration membership is unavailable",
		}
	}

	s.fireRegistrationNotifications(*user)

	return user, nil
}

type AccessRequestResult struct {
	User        *models.AppUser
	Application string
	Status      string
	Created     bool
}

// RequestAccess delegates password verification, identity mapping, and the
// complete membership state machine to Central SSO. SILAPET only reloads the
// resulting read model for its compatible response and business notification.
func (s *AuthService) RequestAccess(
	ctx context.Context,
	email string,
	password string,
) (*AccessRequestResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))

	accessRequest, err := s.centralRequestAccess(
		ctx,
		email,
		password,
	)
	if err != nil {
		return nil, err
	}

	user, err := s.users.GetByEmail(
		ctx,
		email,
	)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, &AuthError{
			StatusCode: http.StatusBadGateway,
			Code:       "INVALID_SSO_RESPONSE",
			Message:    "central access request membership is unavailable",
		}
	}

	if accessRequest.Created {
		s.fireRegistrationNotifications(*user)
	}

	return &AccessRequestResult{
		User:        user,
		Application: accessRequest.Application,
		Status:      accessRequest.Status,
		Created:     accessRequest.Created,
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

func (s *AuthService) fireRegistrationNotifications(user models.AppUser) {
	if s.notify == nil {
		return
	}

	go s.notify.NotifyAdminsForUserEvent(
		context.Background(),
		user,
		"registered",
		"",
	)
	go s.notify.NotifyRegistrationReceived(user)
}
