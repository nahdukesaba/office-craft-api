package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
)

const (
	testAuthUserID = "11111111-1111-4111-8111-111111111111"
	testAppUserID  = "22222222-2222-4222-8222-222222222222"
	testSessionID  = "33333333-3333-4333-8333-333333333333"
	testOtherID    = "44444444-4444-4444-8444-444444444444"
)

type fakeAccessTokenVerifier struct {
	claims *VerifiedAccessToken
	err    error
	calls  int
}

func (f *fakeAccessTokenVerifier) Verify(
	_ context.Context,
	_ string,
) (*VerifiedAccessToken, error) {
	f.calls++
	return f.claims, f.err
}

type fakeAuthenticationContextStore struct {
	identity *models.AuthenticationContext
	err      error
	calls    int

	appUserID       string
	sessionID       string
	applicationCode string
}

func (f *fakeAuthenticationContextStore) GetAuthenticationContext(
	_ context.Context,
	appUserID string,
	sessionID string,
	applicationCode string,
) (*models.AuthenticationContext, error) {
	f.calls++
	f.appUserID = appUserID
	f.sessionID = sessionID
	f.applicationCode = applicationCode
	return f.identity, f.err
}

func validVerifiedAccessToken(role string) *VerifiedAccessToken {
	return &VerifiedAccessToken{
		AuthUserID:  testAuthUserID,
		AppUserID:   testAppUserID,
		SessionID:   testSessionID,
		AppRole:     role,
		LoginSource: models.LoginSourceDirect,
	}
}

func validAuthenticationContext(role string) *models.AuthenticationContext {
	return &models.AuthenticationContext{
		AppUserID:         testAppUserID,
		AuthUserID:        testAuthUserID,
		Email:             "employee@example.com",
		SessionFound:      true,
		SessionUserID:     testAuthUserID,
		ApplicationFound:  true,
		ApplicationCode:   models.ApplicationCodeSilapet,
		ApplicationActive: true,
		MembershipFound:   true,
		MembershipRole:    role,
		MembershipStatus:  models.UserStatusApproved,
	}
}

func newAuthTestApp() *fiber.App {
	return fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			var appErr *apperror.AppError
			if errors.As(err, &appErr) {
				return c.Status(appErr.Status).JSON(appErr)
			}
			return fiber.DefaultErrorHandler(c, err)
		},
	})
}

func performProtectedRequest(
	t *testing.T,
	verifier AccessTokenVerifier,
	store AuthenticationContextStore,
	handler fiber.Handler,
) (*http.Response, apperror.AppError) {
	t.Helper()

	app := newAuthTestApp()
	app.Get("/private", RequireAuth(verifier, store), handler)
	req := httptest.NewRequest(http.MethodGet, "/private", nil)
	req.Header.Set("Authorization", "Bearer access-token")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("perform protected request: %v", err)
	}

	var payload apperror.AppError
	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode error response: %v", err)
		}
	}

	return resp, payload
}

func TestRequireAuthRejectsMissingToken(t *testing.T) {
	app := newAuthTestApp()
	app.Get(
		"/private",
		RequireAuth(&fakeAccessTokenVerifier{}, &fakeAuthenticationContextStore{}),
		func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) },
	)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/private", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestRequireAuthUsesMappedIdentityAndCurrentRole(t *testing.T) {
	for _, role := range []string{models.RoleUser, models.RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			verifier := &fakeAccessTokenVerifier{
				claims: validVerifiedAccessToken(role),
			}
			store := &fakeAuthenticationContextStore{
				identity: validAuthenticationContext(role),
			}

			resp, _ := performProtectedRequest(
				t,
				verifier,
				store,
				func(c *fiber.Ctx) error {
					if UserIDFromCtx(c) != testAppUserID ||
						UserIDFromCtx(c) == testAuthUserID {

						t.Fatal("business user ID did not use app_user_id")
					}
					if RoleFromCtx(c) != role {
						t.Fatal("locals role did not use current membership role")
					}
					if LoginSourceFromCtx(c) != models.LoginSourceDirect {
						t.Fatal("locals login source did not use verified token metadata")
					}
					if email, _ := c.Locals(LocalsEmail).(string); email != "employee@example.com" {
						t.Fatal("locals email did not use resolved profile email")
					}
					return c.SendStatus(http.StatusOK)
				},
			)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}
			if store.appUserID != testAppUserID ||
				store.sessionID != testSessionID ||
				store.applicationCode != models.ApplicationCodeSilapet {

				t.Fatal("authentication context lookup used incorrect identifiers")
			}
		})
	}
}

func TestRequireAuthFailsClosedOnRepositoryError(t *testing.T) {
	resp, payload := performProtectedRequest(
		t,
		&fakeAccessTokenVerifier{claims: validVerifiedAccessToken(models.RoleUser)},
		&fakeAuthenticationContextStore{err: errors.New("database unavailable")},
		func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) },
	)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable || payload.Code != "AUTH_UNAVAILABLE" {
		t.Fatalf("expected fail-closed 503 AUTH_UNAVAILABLE, got %d %q", resp.StatusCode, payload.Code)
	}
}

func TestRequireAuthRejectsInvalidToken(t *testing.T) {
	store := &fakeAuthenticationContextStore{}
	resp, _ := performProtectedRequest(
		t,
		&fakeAccessTokenVerifier{err: errors.New("invalid signature")},
		store,
		func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) },
	)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized || store.calls != 0 {
		t.Fatal("invalid token was not rejected before database lookup")
	}
}

func TestRequireAuthRejectsInvalidCurrentState(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*models.AuthenticationContext)
		claimRole  string
		statusCode int
		code       string
	}{
		{"missing session", func(v *models.AuthenticationContext) { v.SessionFound = false }, models.RoleUser, 401, "UNAUTHORIZED"},
		{"session owned by other user", func(v *models.AuthenticationContext) { v.SessionUserID = testOtherID }, models.RoleUser, 401, "UNAUTHORIZED"},
		{"app user mismatch", func(v *models.AuthenticationContext) { v.AppUserID = testOtherID }, models.RoleUser, 401, "UNAUTHORIZED"},
		{"auth mapping mismatch", func(v *models.AuthenticationContext) { v.AuthUserID = testOtherID }, models.RoleUser, 401, "UNAUTHORIZED"},
		{"deleted app user", func(v *models.AuthenticationContext) { v.UserDeleted = true }, models.RoleUser, 401, "UNAUTHORIZED"},
		{"missing application", func(v *models.AuthenticationContext) { v.ApplicationFound = false }, models.RoleUser, 403, "APPLICATION_ACCESS_UNAVAILABLE"},
		{"inactive application", func(v *models.AuthenticationContext) { v.ApplicationActive = false }, models.RoleUser, 403, "APPLICATION_ACCESS_UNAVAILABLE"},
		{"wrong application", func(v *models.AuthenticationContext) { v.ApplicationCode = "other" }, models.RoleUser, 403, "APPLICATION_ACCESS_UNAVAILABLE"},
		{"missing membership", func(v *models.AuthenticationContext) { v.MembershipFound = false }, models.RoleUser, 403, "APPLICATION_ACCESS_UNAVAILABLE"},
		{"deleted membership", func(v *models.AuthenticationContext) { v.MembershipDeleted = true }, models.RoleUser, 403, "APPLICATION_ACCESS_UNAVAILABLE"},
		{"pending membership", func(v *models.AuthenticationContext) { v.MembershipStatus = models.UserStatusPending }, models.RoleUser, 403, "ACCOUNT_PENDING_APPROVAL"},
		{"rejected membership", func(v *models.AuthenticationContext) { v.MembershipStatus = models.UserStatusRejected }, models.RoleUser, 403, "ACCOUNT_REJECTED"},
		{"invalid role", func(v *models.AuthenticationContext) { v.MembershipRole = "owner" }, "owner", 403, "APPLICATION_ACCESS_UNAVAILABLE"},
		{"stale role", func(v *models.AuthenticationContext) { v.MembershipRole = models.RoleAdmin }, models.RoleUser, 401, "TOKEN_AUTHORIZATION_STALE"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := validAuthenticationContext(models.RoleUser)
			test.mutate(identity)
			resp, payload := performProtectedRequest(
				t,
				&fakeAccessTokenVerifier{claims: validVerifiedAccessToken(test.claimRole)},
				&fakeAuthenticationContextStore{identity: identity},
				func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) },
			)
			defer resp.Body.Close()

			if resp.StatusCode != test.statusCode || payload.Code != test.code {
				t.Fatalf(
					"expected %d %q, got %d %q",
					test.statusCode,
					test.code,
					resp.StatusCode,
					payload.Code,
				)
			}
		})
	}
}

func TestRequireAdminEnforcesApplicationRole(t *testing.T) {
	app := newAuthTestApp()
	app.Get(
		"/admin-only",
		func(c *fiber.Ctx) error {
			c.Locals(LocalsRole, c.Get("X-Test-Role"))
			return c.Next()
		},
		RequireAdmin(),
		func(c *fiber.Ctx) error { return c.SendStatus(http.StatusOK) },
	)

	for _, test := range []struct {
		role   string
		status int
	}{
		{models.RoleAdmin, http.StatusOK},
		{models.RoleUser, http.StatusForbidden},
		{"authenticated", http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, "/admin-only", nil)
		req.Header.Set("X-Test-Role", test.role)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != test.status {
			t.Fatalf("role %q: expected %d, got %d", test.role, test.status, resp.StatusCode)
		}
	}
}
