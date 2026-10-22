package services

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"office-craft-api/internal/models"
)

type BrowserSSOFlowStore interface {
	Create(context.Context, *models.BrowserSSOFlow) error
	Consume(context.Context, []byte) (*models.BrowserSSOFlow, error)
}

type BrowserSSOCompletion struct {
	Session *AuthSession
	Status  string
}

func (s *AuthService) SetBrowserSSOFlowStore(store BrowserSSOFlowStore) {
	s.ssoFlows = store
}

func (s *AuthService) StartBrowserSSO(ctx context.Context) (string, error) {
	key, err := s.browserSSOKey()
	if err != nil || s.ssoFlows == nil {
		return "", browserSSOUnavailable()
	}
	state, err := randomBrowserSSOValue(32)
	if err != nil {
		return "", browserSSOUnavailable()
	}
	if err := s.ssoFlows.Create(ctx, &models.BrowserSSOFlow{
		StateHash: browserSSOHash(state),
		ExpiresAt: time.Now().Add(s.cfg.SSOFlowTTL),
	}); err != nil {
		return "", browserSSOUnavailable()
	}
	verifier := derivePKCEVerifier(key, state)
	challengeHash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeHash[:])

	authorizeURL, err := url.Parse(strings.TrimRight(s.cfg.SSOBaseURL, "/") + "/authorize")
	if err != nil {
		return "", browserSSOUnavailable()
	}
	query := authorizeURL.Query()
	query.Set("client_id", s.cfg.SSOClientID)
	query.Set("redirect_uri", s.cfg.SSORedirectURI)
	query.Set("response_type", "code")
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	authorizeURL.RawQuery = query.Encode()
	return authorizeURL.String(), nil
}

func (s *AuthService) StartBrowserSSOLogout() (string, error) {
	clientID := strings.TrimSpace(s.cfg.SSOClientID)
	postLogoutRedirectURI := strings.TrimSpace(s.cfg.SSOPostLogoutRedirectURI)
	centralURL, err := url.Parse(strings.TrimRight(s.cfg.SSOBaseURL, "/") + "/logout")
	if err != nil || (centralURL.Scheme != "http" && centralURL.Scheme != "https") || centralURL.Host == "" ||
		(clientID == "" || postLogoutRedirectURI == "") {
		return "", browserSSOUnavailable()
	}
	redirect, err := url.Parse(postLogoutRedirectURI)
	if err != nil || (redirect.Scheme != "http" && redirect.Scheme != "https") || redirect.Host == "" {
		return "", browserSSOUnavailable()
	}
	query := centralURL.Query()
	query.Set("client_id", clientID)
	query.Set("post_logout_redirect_uri", postLogoutRedirectURI)
	centralURL.RawQuery = query.Encode()
	return centralURL.String(), nil
}

func (s *AuthService) CompleteBrowserSSO(
	ctx context.Context,
	state string,
	code string,
	status string,
) (*BrowserSSOCompletion, error) {
	state = strings.TrimSpace(state)
	code = strings.TrimSpace(code)
	status = strings.ToLower(strings.TrimSpace(status))
	key, err := s.browserSSOKey()
	if err != nil || s.ssoFlows == nil || state == "" {
		return nil, &AuthError{StatusCode: http.StatusUnauthorized, Code: "INVALID_SSO_STATE", Message: "invalid or expired SSO state"}
	}
	flow, err := s.ssoFlows.Consume(ctx, browserSSOHash(state))
	if err != nil || flow == nil {
		return nil, &AuthError{StatusCode: http.StatusUnauthorized, Code: "INVALID_SSO_STATE", Message: "invalid or expired SSO state"}
	}
	if status != "" {
		switch status {
		case "pending":
			return &BrowserSSOCompletion{Status: "pending"}, nil
		case "denied":
			return nil, &AuthError{StatusCode: http.StatusForbidden, Code: "ACCOUNT_REJECTED", Message: "SILAPET access is not available"}
		default:
			return nil, &AuthError{StatusCode: http.StatusBadRequest, Code: "INVALID_SSO_RESULT", Message: "invalid SSO result"}
		}
	}
	if code == "" {
		return nil, &AuthError{StatusCode: http.StatusBadRequest, Code: "INVALID_SSO_RESULT", Message: "authorization code is required"}
	}
	centralSession, err := s.centralBrowserSSOExchange(ctx, code, derivePKCEVerifier(key, state))
	if err != nil {
		return nil, err
	}
	session, err := s.buildSilapetSession(ctx, centralSession)
	if err != nil {
		s.discardCentralSession(centralSession)
		return nil, err
	}
	return &BrowserSSOCompletion{Session: session, Status: "authenticated"}, nil
}

func (s *AuthService) browserSSOKey() ([]byte, error) {
	value := strings.TrimSpace(s.cfg.SSOFlowHMACKey)
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil || len(key) < 32 || strings.TrimSpace(s.cfg.SSOClientID) == "" ||
		strings.TrimSpace(s.cfg.SSORedirectURI) == "" {
		return nil, errors.New("browser SSO is not configured")
	}
	return key, nil
}

func derivePKCEVerifier(key []byte, state string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("silapet-pkce:" + state))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func randomBrowserSSOValue(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func browserSSOHash(value string) []byte {
	hash := sha256.Sum256([]byte(value))
	return hash[:]
}

func browserSSOUnavailable() *AuthError {
	return &AuthError{StatusCode: http.StatusServiceUnavailable, Code: "SSO_UNAVAILABLE", Message: "browser SSO is unavailable"}
}
