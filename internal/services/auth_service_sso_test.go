package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"office-craft-api/internal/config"
	"office-craft-api/internal/models"
)

type fakeAuthUserStore struct {
	user *models.AppUser
	err  error

	getCalls              int
	getID                 string
	createMembershipCalls int
	updatePhoneCalls      int
}

func (f *fakeAuthUserStore) GetByID(
	_ context.Context,
	id string,
) (*models.AppUser, error) {
	f.getCalls++
	f.getID = id
	return f.user, f.err
}

func (f *fakeAuthUserStore) CreateMembership(
	_ context.Context,
	_ string,
	_ string,
	_ string,
) (*models.AppUser, bool, error) {
	f.createMembershipCalls++
	return f.user, true, f.err
}

func (f *fakeAuthUserStore) UpdatePhone(
	_ context.Context,
	_ string,
	_ string,
) (*models.AppUser, error) {
	f.updatePhoneCalls++
	return f.user, f.err
}

type fakeAuthNotifier struct {
	mu                 sync.Mutex
	adminNotifications int
	userNotifications  int
	events             chan string
}

func newFakeAuthNotifier() *fakeAuthNotifier {
	return &fakeAuthNotifier{events: make(chan string, 4)}
}

func (f *fakeAuthNotifier) NotifyAdminsForUserEvent(
	_ context.Context,
	_ models.AppUser,
	_ string,
	_ string,
) {
	f.mu.Lock()
	f.adminNotifications++
	f.mu.Unlock()
	f.events <- "admin"
}

func (f *fakeAuthNotifier) NotifyRegistrationReceived(_ models.AppUser) {
	f.mu.Lock()
	f.userNotifications++
	f.mu.Unlock()
	f.events <- "user"
}

func (f *fakeAuthNotifier) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.adminNotifications, f.userNotifications
}

func TestRegisterDelegatesIdentityAndMembershipToCentralSSO(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/auth/register" {
			t.Errorf("unexpected Central SSO request %s %s", r.Method, r.URL.Path)
		}

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode registration request: %v", err)
		}
		if payload["application"] != models.ApplicationCodeSilapet {
			t.Errorf("unexpected registration application %q", payload["application"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"user": {
				"id": "22222222-2222-4222-8222-222222222222",
				"email": "employee@example.com",
				"fullName": "Employee Example"
			}
		}`))
	}))
	defer server.Close()

	users := &fakeAuthUserStore{user: &models.AppUser{
		ID:       "22222222-2222-4222-8222-222222222222",
		Email:    "employee@example.com",
		FullName: "Employee Example",
		Role:     models.RoleUser,
		Status:   models.UserStatusPending,
	}}
	notifier := newFakeAuthNotifier()
	service := NewAuthService(
		&config.Config{SSOBaseURL: server.URL},
		users,
		notifier,
	)

	user, err := service.Register(
		context.Background(),
		"employee@example.com",
		"RegistrationPassword123!",
		"Employee Example",
		"",
	)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if requestCount != 1 || user == nil ||
		user.ID != "22222222-2222-4222-8222-222222222222" {

		t.Fatal("registration response did not preserve the Central app_users ID")
	}
	if users.getCalls != 1 || users.getID != user.ID {
		t.Fatal("registration did not reload the Central membership projection")
	}
	if users.createMembershipCalls != 0 {
		t.Fatal("SILAPET created a duplicate membership after Central registration")
	}

	for i := 0; i < 2; i++ {
		select {
		case <-notifier.events:
		case <-time.After(time.Second):
			t.Fatal("registration notification was not dispatched")
		}
	}
	adminCount, userCount := notifier.counts()
	if adminCount != 1 || userCount != 1 {
		t.Fatalf(
			"expected one admin and one user notification, got %d and %d",
			adminCount,
			userCount,
		)
	}
}

func TestAuthLifecycleStillProxiesCentralSSO(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/auth/login", "/api/v1/auth/refresh":
			_, _ = w.Write([]byte(`{
				"accessToken":"supabase-access-token",
				"refreshToken":"supabase-refresh-token",
				"tokenType":"bearer",
				"expiresIn":3600,
				"expiresAt":4102444800,
				"user":{"id":"22222222-2222-4222-8222-222222222222","email":"employee@example.com","fullName":"Employee Example"}
			}`))
		case "/api/v1/auth/logout", "/api/v1/auth/password",
			"/api/v1/auth/forgot-password", "/api/v1/auth/reset-password":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	users := &fakeAuthUserStore{user: &models.AppUser{
		ID:     "22222222-2222-4222-8222-222222222222",
		Email:  "employee@example.com",
		Role:   models.RoleUser,
		Status: models.UserStatusApproved,
	}}
	service := NewAuthService(
		&config.Config{SSOBaseURL: server.URL},
		users,
		newFakeAuthNotifier(),
	)

	login, err := service.Login(context.Background(), "employee@example.com", "password")
	if err != nil || login.AccessToken != "supabase-access-token" {
		t.Fatalf("login proxy failed: %v", err)
	}
	refresh, err := service.Refresh(context.Background(), "supabase-refresh-token")
	if err != nil || refresh.RefreshToken != "supabase-refresh-token" {
		t.Fatalf("refresh proxy failed: %v", err)
	}
	if err := service.Logout(context.Background(), "supabase-access-token"); err != nil {
		t.Fatalf("logout proxy failed: %v", err)
	}
	if err := service.ChangePassword(context.Background(), "supabase-access-token", "old", "new-password"); err != nil {
		t.Fatalf("change password proxy failed: %v", err)
	}
	if err := service.ForgotPassword(context.Background(), "employee@example.com"); err != nil {
		t.Fatalf("forgot password proxy failed: %v", err)
	}
	if err := service.ResetPassword(context.Background(), "recovery-token-hash", "new-password"); err != nil {
		t.Fatalf("reset password proxy failed: %v", err)
	}

	for _, path := range []string{
		"/api/v1/auth/login",
		"/api/v1/auth/refresh",
		"/api/v1/auth/logout",
		"/api/v1/auth/password",
		"/api/v1/auth/forgot-password",
		"/api/v1/auth/reset-password",
	} {
		mu.Lock()
		count := calls[path]
		mu.Unlock()
		if count != 1 {
			t.Fatalf("expected one Central SSO call to %s, got %d", path, count)
		}
	}
}

func TestRequestAccessDocumentsCentralMembershipDependency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{
			"error":"account_unavailable",
			"message":"SILAPET membership is required"
		}`))
	}))
	defer server.Close()

	users := &fakeAuthUserStore{}
	service := NewAuthService(
		&config.Config{SSOBaseURL: server.URL},
		users,
		newFakeAuthNotifier(),
	)

	_, err := service.RequestAccess(
		context.Background(),
		"employee@example.com",
		"password",
	)
	authErr, ok := err.(*AuthError)
	if !ok || authErr.Code != "ACCOUNT_UNAVAILABLE" {
		t.Fatalf("expected documented Central membership rejection, got %v", err)
	}
	if users.createMembershipCalls != 0 {
		t.Fatal("request-access created membership after Central rejected authentication")
	}
}

type fakeResourceSeedStore struct {
	count       int64
	countCalls  int
	createCalls int
}

func (f *fakeResourceSeedStore) Count(_ context.Context) (int64, error) {
	f.countCalls++
	return f.count, nil
}

func (f *fakeResourceSeedStore) Create(
	_ context.Context,
	_ models.ResourceInput,
) (*models.Resource, error) {
	f.createCalls++
	return &models.Resource{}, nil
}

func TestNormalSeedPathContainsOnlyBusinessResources(t *testing.T) {
	resources := &fakeResourceSeedStore{}
	service := NewSeedService(resources)
	service.Run(context.Background())

	if resources.countCalls != 1 || resources.createCalls != 4 {
		t.Fatalf(
			"expected resource-only seed path, got %d count and %d create calls",
			resources.countCalls,
			resources.createCalls,
		)
	}
}
