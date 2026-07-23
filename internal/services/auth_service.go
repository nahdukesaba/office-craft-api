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
}

func NewAuthService(cfg *config.Config, users *repository.UserRepository) *AuthService {
	return &AuthService{
		cfg:    cfg,
		users:  users,
		client: &http.Client{Timeout: 15 * time.Second},
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

// Login authenticates against Supabase Auth's password grant, then enforces
// the admin-approval gate: only "approved" accounts get a usable token back.
func (s *AuthService) Login(ctx context.Context, email, password string) (string, *models.AppUser, error) {
	resp, err := s.supabaseRequest(ctx, "/token?grant_type=password", map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return "", nil, err
	}

	user, err := s.users.GetByID(ctx, resp.User.ID)
	if err != nil {
		return "", nil, err
	}
	if user == nil {
		// Profile row missing (e.g. user created directly in Supabase
		// dashboard, bypassing our /auth/register) -> backfill as pending,
		// same gate as any other new account.
		user, err = s.users.Upsert(ctx, resp.User.ID, resp.User.Email, "", nil, models.RoleUser, models.UserStatusPending)
		if err != nil {
			return "", nil, err
		}
	}

	switch user.Status {
	case models.UserStatusPending:
		return "", nil, &AuthError{StatusCode: 403, Code: "ACCOUNT_PENDING_APPROVAL", Message: "your account is awaiting admin approval"}
	case models.UserStatusRejected:
		return "", nil, &AuthError{StatusCode: 403, Code: "ACCOUNT_REJECTED", Message: "your account access request was rejected"}
	}

	return resp.AccessToken, user, nil
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
