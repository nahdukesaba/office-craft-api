package services

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"office-craft-api/internal/models"
)

type centralSSOUser struct {
	ID       string  `json:"id"`
	Email    string  `json:"email"`
	FullName string  `json:"fullName"`
	Phone    *string `json:"phone,omitempty"`
}

type centralSSOSession struct {
	AccessToken  string         `json:"accessToken"`
	RefreshToken string         `json:"refreshToken"`
	TokenType    string         `json:"tokenType"`
	ExpiresIn    int            `json:"expiresIn"`
	ExpiresAt    int64          `json:"expiresAt"`
	User         centralSSOUser `json:"user"`
}

type centralSSOError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// AuthSession is the session shape SILAPET needs from Central SSO.
// User authorization is always resolved locally from SILAPET membership.
type AuthSession struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int
	ExpiresAt    int64
	User         *models.AppUser
}

func (s *AuthService) centralSSORequest(
	ctx context.Context,
	method,
	path string,
	body interface{},
	accessToken string,
	out interface{},
) error {
	var reader io.Reader

	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}

		reader = bytes.NewReader(payload)
	}

	url := strings.TrimRight(s.cfg.SSOBaseURL, "/") + path

	req, err := http.NewRequestWithContext(
		ctx,
		method,
		url,
		reader,
	)
	if err != nil {
		return err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if strings.TrimSpace(accessToken) != "" {
		req.Header.Set(
			"Authorization",
			"Bearer "+strings.TrimSpace(accessToken),
		)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return &AuthError{
			StatusCode: http.StatusServiceUnavailable,
			Code:       "AUTH_UNAVAILABLE",
			Message:    "central authentication service is unavailable",
		}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		var centralErr centralSSOError
		_ = json.Unmarshal(raw, &centralErr)

		return mapCentralSSOError(
			resp.StatusCode,
			centralErr.Error,
			centralErr.Message,
		)
	}

	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}

	return json.Unmarshal(raw, out)
}

func mapCentralSSOError(
	status int,
	code,
	message string,
) error {
	code = strings.TrimSpace(code)
	message = strings.TrimSpace(message)

	localCode := strings.ToUpper(code)

	switch code {
	case "invalid_credentials":
		// Keep legacy SILAPET login error code compatible for now.
		localCode = "AUTH_ERROR"

	case "invalid_refresh_token":
		localCode = "INVALID_REFRESH_TOKEN"

	case "invalid_token", "missing_token":
		localCode = "INVALID_TOKEN"

	case "rate_limited":
		localCode = "RATE_LIMITED"

	case "auth_unavailable":
		localCode = "AUTH_UNAVAILABLE"

	case "account_unavailable":
		localCode = "ACCOUNT_UNAVAILABLE"

	case "account_pending":
		localCode = "ACCOUNT_PENDING_APPROVAL"

	case "account_rejected":
		localCode = "ACCOUNT_REJECTED"
	}

	if localCode == "" {
		localCode = "SSO_ERROR"
	}

	if message == "" {
		message = "central authentication request failed"
	}

	return &AuthError{
		StatusCode: status,
		Code:       localCode,
		Message:    message,
	}
}

func (s *AuthService) centralLogin(
	ctx context.Context,
	email,
	password string,
) (*centralSSOSession, error) {
	var session centralSSOSession

	err := s.centralSSORequest(
		ctx,
		http.MethodPost,
		"/api/v1/auth/login",
		map[string]string{
			"email":    email,
			"password": password,
		},
		"",
		&session,
	)
	if err != nil {
		return nil, err
	}

	return &session, nil
}

func (s *AuthService) centralRefresh(
	ctx context.Context,
	refreshToken string,
) (*centralSSOSession, error) {
	var session centralSSOSession

	err := s.centralSSORequest(
		ctx,
		http.MethodPost,
		"/api/v1/auth/refresh",
		map[string]string{
			"refreshToken": refreshToken,
		},
		"",
		&session,
	)
	if err != nil {
		return nil, err
	}

	return &session, nil
}

func (s *AuthService) centralLogout(
	ctx context.Context,
	accessToken string,
) error {
	return s.centralSSORequest(
		ctx,
		http.MethodPost,
		"/api/v1/auth/logout",
		nil,
		accessToken,
		nil,
	)
}
