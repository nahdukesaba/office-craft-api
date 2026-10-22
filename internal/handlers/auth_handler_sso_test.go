package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/config"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
	"office-craft-api/internal/services"
)

const (
	handlerTestAppUserID  = "22222222-2222-4222-8222-222222222222"
	handlerTestAuthUserID = "11111111-1111-4111-8111-111111111111"
)

type fakeHandlerAuthUsers struct {
	user                  *models.AppUser
	getID                 string
	getEmail              string
	createMembershipCalls int
}

func (f *fakeHandlerAuthUsers) GetByID(
	_ context.Context,
	id string,
) (*models.AppUser, error) {
	f.getID = id
	return f.user, nil
}

func (f *fakeHandlerAuthUsers) GetByEmail(
	_ context.Context,
	email string,
) (*models.AppUser, error) {
	f.getEmail = email
	return f.user, nil
}

func (f *fakeHandlerAuthUsers) CreateMembership(
	_ context.Context,
	_ string,
	_ string,
	_ string,
) (*models.AppUser, bool, error) {
	f.createMembershipCalls++
	return f.user, true, nil
}

func (f *fakeHandlerAuthUsers) UpdatePhone(
	_ context.Context,
	_ string,
	_ string,
) (*models.AppUser, error) {
	return f.user, nil
}

type noopHandlerAuthNotifier struct{}

func (noopHandlerAuthNotifier) NotifyAdminsForUserEvent(
	context.Context,
	models.AppUser,
	string,
	string,
) {
}

func (noopHandlerAuthNotifier) NotifyRegistrationReceived(models.AppUser) {}

func TestSSOLogoutStartRedirectsToConfiguredCentralFlow(t *testing.T) {
	users := &fakeHandlerAuthUsers{}
	authService := services.NewAuthService(
		&config.Config{
			SSOBaseURL:               "http://127.0.0.1:18082",
			SSOClientID:              "silapet-web",
			SSOPostLogoutRedirectURI: "http://localhost:8081/login",
		},
		users,
		noopHandlerAuthNotifier{},
	)
	handler := NewAuthHandler(authService, users)
	app := fiber.New()
	app.Get("/api/auth/sso/logout", handler.SSOLogoutStart)

	response, err := app.Test(httptest.NewRequest(
		http.MethodGet,
		"/api/auth/sso/logout",
		nil,
	))
	if err != nil {
		t.Fatal(err)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusFound || location.Path != "/logout" ||
		location.Query().Get("client_id") != "silapet-web" ||
		location.Query().Get("post_logout_redirect_uri") != "http://localhost:8081/login" {
		t.Fatalf("unexpected coordinated logout redirect: %d %q", response.StatusCode, location.String())
	}
}

func TestLogoutResponseUsesVerifiedLoginSource(t *testing.T) {
	for _, test := range []struct {
		name            string
		loginSource     models.LoginSource
		centralRequired bool
	}{
		{name: "direct login", loginSource: models.LoginSourceDirect, centralRequired: false},
		{name: "browser SSO login", loginSource: models.LoginSourceSSO, centralRequired: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			logoutCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logoutCalls++
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/auth/logout" ||
					r.Header.Get("Authorization") != "Bearer verified-access-token" {
					t.Errorf("unexpected Central logout request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			users := &fakeHandlerAuthUsers{}
			handler := NewAuthHandler(
				services.NewAuthService(&config.Config{SSOBaseURL: server.URL}, users, noopHandlerAuthNotifier{}),
				users,
			)
			app := fiber.New()
			app.Post(
				"/api/auth/logout",
				func(c *fiber.Ctx) error {
					c.Locals(middleware.LocalsLoginSource, test.loginSource)
					return c.Next()
				},
				handler.Logout,
			)

			request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
			request.Header.Set("Authorization", "Bearer verified-access-token")
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var payload struct {
				CentralLogoutRequired bool `json:"centralLogoutRequired"`
			}
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || logoutCalls != 1 ||
				payload.CentralLogoutRequired != test.centralRequired {
				t.Fatalf("unexpected logout result: status=%d calls=%d payload=%#v",
					response.StatusCode, logoutCalls, payload)
			}
		})
	}
}

func TestRegisterResponseKeepsInternalUserContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	users := &fakeHandlerAuthUsers{user: &models.AppUser{
		ID:       handlerTestAppUserID,
		Email:    "employee@example.com",
		FullName: "Employee Example",
		Role:     models.RoleUser,
		Status:   models.UserStatusPending,
	}}
	authService := services.NewAuthService(
		&config.Config{SSOBaseURL: server.URL},
		users,
		noopHandlerAuthNotifier{},
	)
	handler := NewAuthHandler(authService, users)
	app := fiber.New()
	app.Post("/api/auth/register", handler.Register)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/register",
		strings.NewReader(`{
			"email":"employee@example.com",
			"password":"RegistrationPassword123!",
			"fullName":"Employee Example",
			"application":"silapet"
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("perform registration: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var payload struct {
		Message string         `json:"message"`
		User    models.AppUser `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode registration response: %v", err)
	}
	if payload.Message != "account created and awaiting admin approval" ||
		payload.User.ID != handlerTestAppUserID ||
		payload.User.ID == handlerTestAuthUserID ||
		payload.User.Status != models.UserStatusPending {

		t.Fatal("registration response contract or internal identity changed")
	}
	if users.createMembershipCalls != 0 {
		t.Fatal("handler registration caused duplicate membership creation")
	}
}

func TestRequestAccessPreservesPublicContractWithoutTokens(t *testing.T) {
	tests := []struct {
		name           string
		created        bool
		expectedStatus int
		expectedMsg    string
	}{
		{
			name:           "new or reopened request",
			created:        true,
			expectedStatus: http.StatusCreated,
			expectedMsg:    "SILAPET access request submitted",
		},
		{
			name:           "already pending request",
			created:        false,
			expectedStatus: http.StatusOK,
			expectedMsg:    "SILAPET access request is already pending",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/auth/request-access" {
					t.Errorf("unexpected Central path %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"message":     "Permintaan akses berhasil dikirim.",
					"application": "silapet",
					"status":      "pending",
					"created":     test.created,
				})
			}))
			defer server.Close()

			users := &fakeHandlerAuthUsers{user: &models.AppUser{
				ID:       handlerTestAppUserID,
				Email:    "employee@example.com",
				FullName: "Employee Example",
				Role:     models.RoleUser,
				Status:   models.UserStatusPending,
			}}
			authService := services.NewAuthService(
				&config.Config{SSOBaseURL: server.URL},
				users,
				noopHandlerAuthNotifier{},
			)
			handler := NewAuthHandler(authService, users)
			app := fiber.New()
			app.Post("/api/auth/request-access", handler.RequestAccess)

			request := httptest.NewRequest(
				http.MethodPost,
				"/api/auth/request-access",
				strings.NewReader(`{
					"email":"employee@example.com",
					"password":"password"
				}`),
			)
			request.Header.Set("Content-Type", "application/json")
			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("perform request-access: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != test.expectedStatus {
				t.Fatalf("expected status %d, got %d", test.expectedStatus, response.StatusCode)
			}
			var payload map[string]any
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload["message"] != test.expectedMsg ||
				payload["application"] != "silapet" ||
				payload["status"] != "pending" ||
				payload["created"] != test.created {

				t.Fatalf("unexpected response: %#v", payload)
			}
			for _, forbidden := range []string{
				"token",
				"accessToken",
				"refreshToken",
			} {
				if _, exists := payload[forbidden]; exists {
					t.Fatalf("response exposed %q", forbidden)
				}
			}
			if users.createMembershipCalls != 0 {
				t.Fatal("handler request-access created local membership")
			}
		})
	}
}

func TestRequestAccessApprovedKeepsCompatibleConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{
			"error":"application_access_exists",
			"message":"Akun sudah memiliki akses ke aplikasi ini."
		}`))
	}))
	defer server.Close()

	users := &fakeHandlerAuthUsers{}
	authService := services.NewAuthService(
		&config.Config{SSOBaseURL: server.URL},
		users,
		noopHandlerAuthNotifier{},
	)
	handler := NewAuthHandler(authService, users)
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			var appErr *apperror.AppError
			if !errors.As(err, &appErr) {
				return c.SendStatus(http.StatusInternalServerError)
			}
			return c.Status(appErr.Status).JSON(appErr)
		},
	})
	app.Post("/api/auth/request-access", handler.RequestAccess)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/request-access",
		strings.NewReader(`{
			"email":"employee@example.com",
			"password":"password"
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("perform request-access: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", response.StatusCode)
	}
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode conflict response: %v", err)
	}
	if payload.Error != "APPLICATION_ACCESS_EXISTS" {
		t.Fatalf("unexpected error code %q", payload.Error)
	}
}

func TestMeResolvesInternalAppUserID(t *testing.T) {
	users := &fakeHandlerAuthUsers{user: &models.AppUser{
		ID:     handlerTestAppUserID,
		Email:  "employee@example.com",
		Role:   models.RoleUser,
		Status: models.UserStatusApproved,
	}}
	handler := NewAuthHandler(nil, users)
	app := fiber.New()
	app.Get(
		"/api/auth/me",
		func(c *fiber.Ctx) error {
			c.Locals(middleware.LocalsUserID, handlerTestAppUserID)
			return c.Next()
		},
		handler.Me,
	)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
	if err != nil {
		t.Fatalf("perform me request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK || users.getID != handlerTestAppUserID {
		t.Fatal("current user lookup did not use internal app_users ID")
	}
}

func TestBookingOwnershipUsesInternalAppUserID(t *testing.T) {
	handler := &BookingHandler{}
	booking := &models.Booking{UserID: handlerTestAppUserID}
	app := fiber.New()
	app.Get("/booking", func(c *fiber.Ctx) error {
		c.Locals(middleware.LocalsRole, models.RoleUser)
		c.Locals(middleware.LocalsUserID, c.Get("X-Test-User-ID"))
		if !handler.canAccess(c, booking) {
			return c.SendStatus(http.StatusForbidden)
		}
		return c.SendStatus(http.StatusOK)
	})

	for _, test := range []struct {
		userID string
		status int
	}{
		{handlerTestAppUserID, http.StatusOK},
		{handlerTestAuthUserID, http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "/booking", nil)
		req.Header.Set("X-Test-User-ID", test.userID)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != test.status {
			t.Fatalf("user %s: expected %d, got %d", test.userID, test.status, resp.StatusCode)
		}
	}
}
