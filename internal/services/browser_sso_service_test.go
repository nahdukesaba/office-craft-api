package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"office-craft-api/internal/config"
	"office-craft-api/internal/models"
)

type fakeSilapetSSOFlowStore struct {
	flow     *models.BrowserSSOFlow
	consumed bool
}

func (f *fakeSilapetSSOFlowStore) Create(_ context.Context, flow *models.BrowserSSOFlow) error {
	f.flow = flow
	return nil
}

func (f *fakeSilapetSSOFlowStore) Consume(_ context.Context, hash []byte) (*models.BrowserSSOFlow, error) {
	if f.consumed || f.flow == nil || string(f.flow.StateHash) != string(hash) || !f.flow.ExpiresAt.After(time.Now()) {
		return nil, nil
	}
	f.consumed = true
	return f.flow, nil
}

type fakeSilapetSSOUsers struct{ user *models.AppUser }

func (f *fakeSilapetSSOUsers) GetByID(context.Context, string) (*models.AppUser, error) {
	return f.user, nil
}
func (f *fakeSilapetSSOUsers) GetByEmail(context.Context, string) (*models.AppUser, error) {
	return f.user, nil
}
func (f *fakeSilapetSSOUsers) UpdatePhone(context.Context, string, string) (*models.AppUser, error) {
	return f.user, nil
}

func testSilapetSSOService(serverURL string) (*AuthService, *fakeSilapetSSOFlowStore) {
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	store := &fakeSilapetSSOFlowStore{}
	service := NewAuthService(&config.Config{
		SSOBaseURL: serverURL, SSOClientID: "silapet-web",
		SSORedirectURI:           "http://localhost:8081/auth/sso/callback",
		SSOPostLogoutRedirectURI: "http://localhost:8081/login",
		SSOFlowHMACKey:           key, SSOFlowTTL: 5 * time.Minute,
	}, &fakeSilapetSSOUsers{user: &models.AppUser{ID: "user-id", Status: models.UserStatusApproved, Role: models.RoleUser}}, nil)
	service.SetBrowserSSOFlowStore(store)
	return service, store
}

func TestBrowserSSOLogoutStartUsesConfiguredRegisteredValues(t *testing.T) {
	service, _ := testSilapetSSOService("http://127.0.0.1:18082")
	logoutURL, err := service.StartBrowserSSOLogout()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(logoutURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "http" || parsed.Host != "127.0.0.1:18082" || parsed.Path != "/logout" ||
		parsed.Query().Get("client_id") != "silapet-web" ||
		parsed.Query().Get("post_logout_redirect_uri") != "http://localhost:8081/login" {
		t.Fatalf("unexpected coordinated logout URL: %q", logoutURL)
	}
}

func TestBrowserSSOStartGeneratesStateAndPKCEWithoutVerifierLeak(t *testing.T) {
	service, store := testSilapetSSOService("http://127.0.0.1:18082")
	authorizeURL, err := service.StartBrowserSSO(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") == "" || query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("missing secure authorize parameters: %s", parsed.RawQuery)
	}
	if query.Get("code_verifier") != "" || query.Get("access_token") != "" || query.Get("refresh_token") != "" {
		t.Fatal("authorize URL leaked verifier or token")
	}
	if store.flow == nil || string(store.flow.StateHash) == query.Get("state") {
		t.Fatal("state was not persisted as a hash")
	}
}

func TestBrowserSSOExchangeRejectsInvalidAndReplayedState(t *testing.T) {
	service, _ := testSilapetSSOService("http://127.0.0.1:18082")
	if _, err := service.CompleteBrowserSSO(context.Background(), "unknown", "code", ""); err == nil {
		t.Fatal("expected invalid state rejection")
	}
	authorizeURL, err := service.StartBrowserSSO(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorizeURL)
	state := parsed.Query().Get("state")
	result, err := service.CompleteBrowserSSO(context.Background(), state, "", "pending")
	if err != nil || result.Status != "pending" {
		t.Fatalf("pending result: %#v %v", result, err)
	}
	if _, err := service.CompleteBrowserSSO(context.Background(), state, "", "pending"); err == nil {
		t.Fatal("expected state replay rejection")
	}
}

func TestBrowserSSOExchangeIsServerToServerAndReturnsNormalSession(t *testing.T) {
	var expectedVerifier string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/sso/exchange" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["code"] != "central-code" || request["codeVerifier"] != expectedVerifier {
			t.Fatalf("unexpected exchange request: %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accessToken":"access","refreshToken":"refresh","tokenType":"bearer","expiresIn":3600,"expiresAt":9999999999,"user":{"id":"user-id","email":"person@example.com"}}`))
	}))
	defer server.Close()

	service, _ := testSilapetSSOService(server.URL)
	authorizeURL, err := service.StartBrowserSSO(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorizeURL)
	state := parsed.Query().Get("state")
	key, _ := service.browserSSOKey()
	expectedVerifier = derivePKCEVerifier(key, state)

	result, err := service.CompleteBrowserSSO(context.Background(), state, "central-code", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Session == nil || result.Session.AccessToken != "access" || result.Session.User.ID != "user-id" {
		t.Fatalf("unexpected session: %#v", result)
	}
}
