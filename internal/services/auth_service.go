package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		user, err = s.users.Upsert(ctx, resp.User.ID, resp.User.Email, "", models.RoleUser, models.UserStatusPending)
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
func (s *AuthService) Register(ctx context.Context, email, password, fullName string) (*models.AppUser, error) {
	resp, err := s.supabaseRequest(ctx, "/signup", map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return nil, err
	}

	user, err := s.users.Upsert(ctx, resp.User.ID, resp.User.Email, fullName, models.RoleUser, models.UserStatusPending)
	if err != nil {
		return nil, err
	}

	return user, nil
}
