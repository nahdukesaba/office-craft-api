package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
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

type supabaseAuthResponse struct {
	AccessToken string `json:"access_token"`
	User        struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

type supabaseErrorResponse struct {
	Msg       string `json:"msg"`
	ErrorCode string `json:"error_code"`
	ErrorDesc string `json:"error_description"`
}

func (s *AuthService) supabaseRequest(ctx context.Context, path string, body interface{}) (*supabaseAuthResponse, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/auth/v1%s", s.cfg.SupabaseURL, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", s.cfg.SupabaseAnonKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		var e supabaseErrorResponse
		_ = json.Unmarshal(raw, &e)
		msg := e.Msg
		if msg == "" {
			msg = e.ErrorDesc
		}
		if msg == "" {
			msg = "authentication request failed"
		}
		return nil, &AuthError{StatusCode: resp.StatusCode, Code: "AUTH_ERROR", Message: msg}
	}

	var out supabaseAuthResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
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

// Register creates a new Supabase Auth user and a matching app_users
// profile with status "pending". No usable token is ever returned here,
// regardless of whether Supabase's own email-confirmation is on or off -
// the account still needs an admin to approve it before anyone can log in.
// phone is optional (pass "" if not supplied) - it's needed later for
// WhatsApp notifications but never blocks registration.
func (s *AuthService) Register(ctx context.Context, email, password, fullName, phone string) (*models.AppUser, error) {
	resp, err := s.supabaseRequest(ctx, "/signup", map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return nil, err
	}

	var phonePtr *string
	if strings.HasPrefix(phone, "0") {
		phone = "62" + phone[1:]
	}
	if phone != "" {
		phonePtr = &phone
	}

	user, err := s.users.Upsert(ctx, resp.User.ID, resp.User.Email, fullName, phonePtr, models.RoleUser, models.UserStatusPending)
	if err != nil {
		return nil, err
	}
	s.fireAdminNotify(*user, "registered", "")
	go s.notify.NotifyRegistrationReceived(*user)
	return user, nil
}

// ChangePassword is the self-service path: verifies oldPassword is correct
// (by attempting a login grant with it) before updating to newPassword
// using the caller's own access token. No service role key involved - this
// only ever acts on the authenticated caller's own account.
func (s *AuthService) ChangePassword(ctx context.Context, accessToken, email, oldPassword, newPassword string) error {
	if _, err := s.supabaseRequest(ctx, "/token?grant_type=password", map[string]string{
		"email":    email,
		"password": oldPassword,
	}); err != nil {
		return &AuthError{StatusCode: 401, Code: "INVALID_PASSWORD", Message: "current password is incorrect"}
	}

	payload, err := json.Marshal(map[string]string{"password": newPassword})
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/auth/v1/user", s.cfg.SupabaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", s.cfg.SupabaseAnonKey)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e supabaseErrorResponse
		_ = json.Unmarshal(raw, &e)
		msg := e.Msg
		if msg == "" {
			msg = e.ErrorDesc
		}
		if msg == "" {
			msg = "failed to update password"
		}
		return &AuthError{StatusCode: resp.StatusCode, Code: "PASSWORD_UPDATE_FAILED", Message: msg}
	}
	return nil
}

// AdminResetPassword generates a random temporary password for userID and
// sets it directly via the Supabase Admin API (requires
// SUPABASE_SERVICE_ROLE_KEY). Returns the generated password so the caller
// can relay it to the user - the handler also best-effort emails it.
func (s *AuthService) AdminResetPassword(ctx context.Context, userID string) (string, error) {
	if s.cfg.SupabaseServiceKey == "" {
		return "", fmt.Errorf("SUPABASE_SERVICE_ROLE_KEY is not configured - admin password reset is unavailable")
	}

	newPassword, err := generateTempPassword()
	if err != nil {
		return "", err
	}

	payload, err := json.Marshal(map[string]string{"password": newPassword})
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/auth/v1/admin/users/%s", s.cfg.SupabaseURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", s.cfg.SupabaseServiceKey)
	req.Header.Set("Authorization", "Bearer "+s.cfg.SupabaseServiceKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		var e supabaseErrorResponse
		_ = json.Unmarshal(raw, &e)
		msg := e.Msg
		if msg == "" {
			msg = e.ErrorDesc
		}
		if msg == "" {
			msg = "failed to reset password"
		}
		return "", &AuthError{StatusCode: resp.StatusCode, Code: "PASSWORD_RESET_FAILED", Message: msg}
	}

	return newPassword, nil
}

// ChangePhoneNumber updates partial the user's phone number in app_users table
func (s *AuthService) ChangePhoneNumber(ctx context.Context, user *models.AppUser, phone string) error {
	if strings.HasPrefix(phone, "0") {
		phone = "62" + phone[1:]
	}

	_, err := s.users.UpdatePhone(ctx, user.ID, phone)
	return err
}

const tempPasswordChars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%"

// generateTempPassword produces a 14-char cryptographically random password
// - well clear of Supabase's default 6-char minimum, and avoids visually
// ambiguous characters (0/O, 1/l/I) since an admin may need to read this
// aloud or retype it for someone.
func generateTempPassword() (string, error) {
	const length = 14
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(tempPasswordChars))))
		if err != nil {
			return "", err
		}
		b[i] = tempPasswordChars[n.Int64()]
	}
	return string(b), nil
}

// fireAdminNotify is fireNotify's counterpart for the admin-facing
// direction (new request / started / finished) - same background-goroutine
// rationale, different recipient set.
func (s *AuthService) fireAdminNotify(user models.AppUser, kind, note string) {
	go s.notify.NotifyAdminsForUserEvent(context.Background(), user, kind, note)
}
