package middleware

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"office-craft-api/internal/apperror"
)

// newAuthTestApp reproduces the application's AppError HTTP mapping.
// Without this handler, Fiber's default handler returns 500 for AppError.
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

func TestRequireAuthRejectsMissingToken(t *testing.T) {
	app := newAuthTestApp()

	app.Get(
		"/private",
		RequireAuth(nil, ""),
		func(c *fiber.Ctx) error {
			return c.SendStatus(fiber.StatusOK)
		},
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/private",
		nil,
	)

	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf(
			"expected 401 without token, got %d",
			resp.StatusCode,
		)
	}
}

func TestRequireAuthDelegatesToCentralSSO(t *testing.T) {
	cases := []struct {
		name       string
		ssoStatus  int
		ssoBody    string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "invalid token",
			ssoStatus:  http.StatusUnauthorized,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name:       "membership forbidden default",
			ssoStatus:  http.StatusForbidden,
			ssoBody:    `{"error":"account_unavailable","message":"application access unavailable"}`,
			wantStatus: http.StatusForbidden,
			wantCode:   "APPLICATION_ACCESS_UNAVAILABLE",
		},
		{
			name:       "membership pending approval",
			ssoStatus:  http.StatusForbidden,
			ssoBody:    `{"error":"account_pending","message":"pending approval"}`,
			wantStatus: http.StatusForbidden,
			wantCode:   "ACCOUNT_PENDING_APPROVAL",
		},
		{
			name:       "membership rejected",
			ssoStatus:  http.StatusForbidden,
			ssoBody:    `{"error":"account_rejected","message":"rejected"}`,
			wantStatus: http.StatusForbidden,
			wantCode:   "ACCOUNT_REJECTED",
		},
		{
			name:       "central unavailable",
			ssoStatus:  http.StatusServiceUnavailable,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "AUTH_UNAVAILABLE",
		},
		{
			name:       "central internal error",
			ssoStatus:  http.StatusInternalServerError,
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "AUTH_UNAVAILABLE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			protectedHandlerCalled := false

			server := httptest.NewServer(
				http.HandlerFunc(func(
					w http.ResponseWriter,
					r *http.Request,
				) {
					calls++

					if r.URL.Path !=
						"/api/v1/auth/application-context" {
						t.Errorf(
							"unexpected SSO path: %s",
							r.URL.Path,
						)
					}

					if r.URL.Query().Get("application") != "silapet" {
						t.Error("unexpected application")
					}

					if r.Header.Get("Authorization") !=
						"Bearer legacy-style-token" {
						t.Error("unexpected bearer header")
					}

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.ssoStatus)
					if tc.ssoBody != "" {
						_, _ = w.Write([]byte(tc.ssoBody))
					}
				}),
			)
			defer server.Close()

			app := newAuthTestApp()

			app.Get(
				"/private",
				RequireAuth(nil, server.URL),
				func(c *fiber.Ctx) error {
					protectedHandlerCalled = true
					return c.SendStatus(fiber.StatusOK)
				},
			)

			req := httptest.NewRequest(
				http.MethodGet,
				"/private",
				nil,
			)

			req.Header.Set(
				"Authorization",
				"Bearer legacy-style-token",
			)

			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Fatalf(
					"expected %d, got %d",
					tc.wantStatus,
					resp.StatusCode,
				)
			}

			var errBody apperror.AppError
			if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
				t.Fatalf("failed to decode error body: %v", err)
			}
			if errBody.Code != tc.wantCode {
				t.Fatalf("expected error code %q, got %q", tc.wantCode, errBody.Code)
			}

			if calls != 1 {
				t.Fatalf(
					"expected one central request, got %d",
					calls,
				)
			}

			if protectedHandlerCalled {
				t.Fatal(
					"protected handler must not run when SSO rejects",
				)
			}
		})
	}
}

func TestRequireAdminEnforcesRole(t *testing.T) {
	app := newAuthTestApp()
	app.Get(
		"/admin-only",
		func(c *fiber.Ctx) error {
			if role := c.Get("X-Test-Role"); role != "" {
				c.Locals(LocalsRole, role)
			}
			return c.Next()
		},
		RequireAdmin(),
		func(c *fiber.Ctx) error {
			return c.SendStatus(fiber.StatusOK)
		},
	)

	userReq := httptest.NewRequest(http.MethodGet, "/admin-only", nil)
	userReq.Header.Set("X-Test-Role", "user")
	userResp, err := app.Test(userReq)
	if err != nil {
		t.Fatal(err)
	}
	defer userResp.Body.Close()
	if userResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for user role, got %d", userResp.StatusCode)
	}

	adminReq := httptest.NewRequest(http.MethodGet, "/admin-only", nil)
	adminReq.Header.Set("X-Test-Role", "admin")
	adminResp, err := app.Test(adminReq)
	if err != nil {
		t.Fatal(err)
	}
	defer adminResp.Body.Close()
	if adminResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for admin role, got %d", adminResp.StatusCode)
	}
}
