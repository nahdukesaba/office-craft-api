package middleware

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
	"office-craft-api/internal/services"
)

type fakeCentralSessionIntrospector struct {
	active    bool
	err       error
	calls     int
	sid       string
	appUserID string
}

func (f *fakeCentralSessionIntrospector) IntrospectBrowserSSO(
	_ context.Context,
	sid string,
	appUserID string,
) (bool, error) {
	f.calls++
	f.sid = sid
	f.appUserID = appUserID
	return f.active, f.err
}

func testMiddlewareSessionManager(t *testing.T) *services.AppSessionManager {
	t.Helper()
	manager, err := services.NewAppSessionManager(
		base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901")),
		false,
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func testSessionCookie(t *testing.T, manager *services.AppSessionManager, source models.LoginSource, sid string) *http.Cookie {
	t.Helper()
	value, err := manager.Encode(services.AppSessionData{
		AppUserID: "app-user", Role: models.RoleUser, Status: models.UserStatusApproved,
		LoginSource: source, SID: sid, AccessToken: "access", RefreshToken: "refresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: services.AppSessionCookieName, Value: value}
}

func TestRequireAppSessionIntrospectsOnlySSOSessions(t *testing.T) {
	manager := testMiddlewareSessionManager(t)
	introspector := &fakeCentralSessionIntrospector{active: true}
	app := fiber.New()
	app.Get("/protected", RequireAppSession(manager, introspector), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusNoContent)
	})

	direct := httptest.NewRequest(http.MethodGet, "/protected", nil)
	direct.AddCookie(testSessionCookie(t, manager, models.LoginSourceDirect, ""))
	directResponse, err := app.Test(direct)
	if err != nil || directResponse.StatusCode != http.StatusNoContent || introspector.calls != 0 {
		t.Fatalf("direct session was coupled to Central SID: status=%d calls=%d err=%v", directResponse.StatusCode, introspector.calls, err)
	}

	sso := httptest.NewRequest(http.MethodGet, "/protected", nil)
	sso.AddCookie(testSessionCookie(t, manager, models.LoginSourceSSO, "trusted-sid"))
	ssoResponse, err := app.Test(sso)
	if err != nil || ssoResponse.StatusCode != http.StatusNoContent || introspector.calls != 1 ||
		introspector.sid != "trusted-sid" || introspector.appUserID != "app-user" {
		t.Fatalf("SSO session binding was not introspected: status=%d fake=%#v err=%v", ssoResponse.StatusCode, introspector, err)
	}
}

func TestRequireAppSessionClearsRevokedOrUnavailableSSOSession(t *testing.T) {
	for _, test := range []struct {
		name   string
		active bool
		err    error
	}{
		{name: "revoked", active: false},
		{name: "unavailable", err: errors.New("central unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := testMiddlewareSessionManager(t)
			introspector := &fakeCentralSessionIntrospector{active: test.active, err: test.err}
			app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
				var appErr *apperror.AppError
				if !errors.As(err, &appErr) {
					return c.SendStatus(http.StatusInternalServerError)
				}
				return c.Status(appErr.Status).JSON(appErr)
			}})
			app.Get("/protected", RequireAppSession(manager, introspector), func(c *fiber.Ctx) error {
				return c.SendStatus(http.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.AddCookie(testSessionCookie(t, manager, models.LoginSourceSSO, "trusted-sid"))
			response, err := app.Test(request)
			if err != nil || response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("invalid SSO session did not fail closed: status=%d err=%v", response.StatusCode, err)
			}
			cookies := response.Cookies()
			if len(cookies) == 0 || cookies[0].Name != services.AppSessionCookieName || cookies[0].Value != "" {
				t.Fatalf("invalid SSO cookie was not cleared: %#v", cookies)
			}
		})
	}
}
