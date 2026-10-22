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
	getByEmailCalls       int
	getEmail              string
	createMembershipCalls int
	updatePhoneCalls      int
}

func (f *fakeAuthUserStore) GetByEmail(
	_ context.Context,
	email string,
) (*models.AppUser, error) {
	f.getByEmailCalls++
	f.getEmail = email
	return f.user, f.err
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
	if _, err := service.Logout(
		context.Background(),
		"supabase-access-token",
		models.LoginSourceDirect,
	); err != nil {
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

func TestLoginMapsCentralMembershipErrorsWithoutLocalMutation(t *testing.T) {
	tests := []struct {
		name           string
		centralCode    string
		expectedCode   string
		expectedStatus int
	}{
		{
			name:           "auto enrolled pending",
			centralCode:    "account_pending",
			expectedCode:   "ACCOUNT_PENDING_APPROVAL",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "rejected",
			centralCode:    "account_rejected",
			expectedCode:   "ACCOUNT_REJECTED",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "disabled",
			centralCode:    "account_disabled",
			expectedCode:   "ACCOUNT_DISABLED",
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/auth/login" {
					t.Errorf("unexpected Central SSO request %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.expectedStatus)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":   test.centralCode,
					"message": "application membership blocks login",
				})
			}))
			defer server.Close()

			users := &fakeAuthUserStore{}
			service := NewAuthService(
				&config.Config{SSOBaseURL: server.URL},
				users,
				newFakeAuthNotifier(),
			)

			session, err := service.Login(
				context.Background(),
				"employee@example.com",
				"password",
			)
			authErr, ok := err.(*AuthError)
			if !ok || authErr.Code != test.expectedCode ||
				authErr.StatusCode != test.expectedStatus || session != nil {

				t.Fatalf("unexpected login result session=%#v err=%#v", session, err)
			}
			if users.getCalls != 0 || users.createMembershipCalls != 0 {
				t.Fatal("Central login rejection triggered local membership work")
			}
		})
	}
}

func TestRequestAccessDelegatesToCentralAndNotifiesOnceWhenCreated(t *testing.T) {
	var mu sync.Mutex
	calls := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()

		if r.Method != http.MethodPost ||
			r.URL.Path != "/api/v1/auth/request-access" {
			t.Errorf("unexpected Central request %s %s", r.Method, r.URL.Path)
		}

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request-access payload: %v", err)
		}
		if payload["email"] != "employee@example.com" ||
			payload["password"] != "password" ||
			payload["application"] != models.ApplicationCodeSilapet {

			t.Errorf("unexpected request-access payload: %#v", payload)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"message":"Permintaan akses berhasil dikirim.",
			"application":"silapet",
			"status":"pending",
			"created":true
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

	result, err := service.RequestAccess(
		context.Background(),
		" Employee@Example.com ",
		"password",
	)
	if err != nil {
		t.Fatalf("request access: %v", err)
	}
	if result == nil || !result.Created ||
		result.Application != models.ApplicationCodeSilapet ||
		result.Status != models.UserStatusPending || result.User != users.user {

		t.Fatalf("unexpected request-access result: %#v", result)
	}
	if users.getByEmailCalls != 1 ||
		users.getEmail != "employee@example.com" {

		t.Fatal("SILAPET did not reload the read-only membership projection")
	}
	if users.createMembershipCalls != 0 {
		t.Fatal("SILAPET created membership locally")
	}

	for i := 0; i < 2; i++ {
		select {
		case <-notifier.events:
		case <-time.After(time.Second):
			t.Fatal("request-access notification was not dispatched")
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

	mu.Lock()
	defer mu.Unlock()
	if calls["/api/v1/auth/request-access"] != 1 ||
		calls["/api/v1/auth/login"] != 0 ||
		calls["/api/v1/auth/logout"] != 0 {

		t.Fatalf("unexpected Central auth calls: %#v", calls)
	}
}

func TestRequestAccessExistingPendingDoesNotNotifyAgain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"message":"Permintaan akses berhasil dikirim.",
			"application":"silapet",
			"status":"pending",
			"created":false
		}`))
	}))
	defer server.Close()

	users := &fakeAuthUserStore{user: &models.AppUser{
		ID:     "22222222-2222-4222-8222-222222222222",
		Email:  "employee@example.com",
		Role:   models.RoleUser,
		Status: models.UserStatusPending,
	}}
	notifier := newFakeAuthNotifier()
	service := NewAuthService(
		&config.Config{SSOBaseURL: server.URL},
		users,
		notifier,
	)

	result, err := service.RequestAccess(
		context.Background(),
		"employee@example.com",
		"password",
	)
	if err != nil || result == nil || result.Created {
		t.Fatalf("expected idempotent pending success, result=%#v err=%v", result, err)
	}

	select {
	case event := <-notifier.events:
		t.Fatalf("existing pending request sent duplicate %s notification", event)
	case <-time.After(100 * time.Millisecond):
	}
	adminCount, userCount := notifier.counts()
	if adminCount != 0 || userCount != 0 {
		t.Fatalf("expected no notifications, got %d and %d", adminCount, userCount)
	}
	if users.createMembershipCalls != 0 {
		t.Fatal("existing pending request mutated membership locally")
	}
}

func TestRequestAccessMapsCentralErrors(t *testing.T) {
	tests := []struct {
		name           string
		centralStatus  int
		centralCode    string
		expectedStatus int
		expectedCode   string
	}{
		{
			name:           "approved membership",
			centralStatus:  http.StatusConflict,
			centralCode:    "application_access_exists",
			expectedStatus: http.StatusConflict,
			expectedCode:   "APPLICATION_ACCESS_EXISTS",
		},
		{
			name:           "wrong password",
			centralStatus:  http.StatusUnauthorized,
			centralCode:    "invalid_credentials",
			expectedStatus: http.StatusUnauthorized,
			expectedCode:   "AUTH_ERROR",
		},
		{
			name:           "rejected or disabled membership",
			centralStatus:  http.StatusForbidden,
			centralCode:    "application_access_blocked",
			expectedStatus: http.StatusForbidden,
			expectedCode:   "APPLICATION_ACCESS_BLOCKED",
		},
		{
			name:           "central unavailable",
			centralStatus:  http.StatusServiceUnavailable,
			centralCode:    "auth_unavailable",
			expectedStatus: http.StatusServiceUnavailable,
			expectedCode:   "AUTH_UNAVAILABLE",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := make(map[string]int)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths[r.URL.Path]++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.centralStatus)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":   test.centralCode,
					"message": "central request rejected",
				})
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
			if !ok || authErr.StatusCode != test.expectedStatus ||
				authErr.Code != test.expectedCode {

				t.Fatalf("unexpected mapped error: %#v", err)
			}
			if users.getByEmailCalls != 0 || users.createMembershipCalls != 0 {
				t.Fatal("Central rejection triggered local membership work")
			}
			if paths["/api/v1/auth/request-access"] != 1 ||
				paths["/api/v1/auth/login"] != 0 ||
				paths["/api/v1/auth/logout"] != 0 {

				t.Fatalf("unexpected Central paths: %#v", paths)
			}
		})
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
