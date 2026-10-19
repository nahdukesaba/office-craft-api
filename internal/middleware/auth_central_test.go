package middleware

import (
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
		wantStatus int
	}{
		{
			name:       "invalid token",
			ssoStatus:  http.StatusUnauthorized,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "membership forbidden",
			ssoStatus:  http.StatusForbidden,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "central unavailable",
			ssoStatus:  http.StatusServiceUnavailable,
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "central internal error",
			ssoStatus:  http.StatusInternalServerError,
			wantStatus: http.StatusServiceUnavailable,
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

					w.WriteHeader(tc.ssoStatus)
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
