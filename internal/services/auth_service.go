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
		return nil, &AuthError{StatusCode: resp.StatusCode, Message: msg}
	}

	var out supabaseAuthResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AuthError carries the HTTP status Supabase returned so handlers can relay it.
type AuthError struct {
	StatusCode int
	Message    string
}

func (e *AuthError) Error() string { return e.Message }

// Login authenticates against Supabase Auth's password grant and syncs the
// local profile row, returning the access token and AppUser.
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
		// Profile row missing (e.g. user created directly in Supabase dashboard) -> backfill.
		user, err = s.users.Upsert(ctx, resp.User.ID, resp.User.Email, "", models.RoleUser)
		if err != nil {
			return "", nil, err
		}
	}

	return resp.AccessToken, user, nil
}

// Register creates a new Supabase Auth user and a matching app_users profile.
func (s *AuthService) Register(ctx context.Context, email, password, fullName string) (string, *models.AppUser, error) {
	resp, err := s.supabaseRequest(ctx, "/signup", map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return "", nil, err
	}

	user, err := s.users.Upsert(ctx, resp.User.ID, resp.User.Email, fullName, models.RoleUser)
	if err != nil {
		return "", nil, err
	}

	return resp.AccessToken, user, nil
}
