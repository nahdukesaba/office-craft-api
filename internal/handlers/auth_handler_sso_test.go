package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

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
	createMembershipCalls int
}

func (f *fakeHandlerAuthUsers) GetByID(
	_ context.Context,
	id string,
) (*models.AppUser, error) {
	f.getID = id
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
