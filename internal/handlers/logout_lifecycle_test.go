package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"office-craft-api/internal/apperror"
	"office-craft-api/internal/config"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
	"office-craft-api/internal/services"
)

func TestSSOLogoutCancelPreservesSessionAndConfirmInvalidatesOnlySSO(t *testing.T) {
	var active atomic.Bool
	active.Store(true)
	var introspections, tokenLogouts atomic.Int32
	central := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/logout" {
			tokenLogouts.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/api/v1/auth/sso/introspect" {
			t.Errorf("unexpected Central request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		introspections.Add(1)
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["sid"] != "central-family" || body["appUserId"] != handlerTestAppUserID {
			t.Error("introspection lost the server-owned session binding")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"active": active.Load()})
	}))
	defer central.Close()

	manager, err := services.NewAppSessionManager(
		base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901")), false, time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	users := &fakeHandlerAuthUsers{}
	auth := services.NewAuthService(&config.Config{
		SSOBaseURL: central.URL, SSOClientID: "silapet-web",
		SSOIntrospectionSecret: "test-introspection-secret-at-least-32-characters",
	}, users, noopHandlerAuthNotifier{})
	handler := NewAuthHandler(auth, users, manager)
	app := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
		var ae *apperror.AppError
		if errors.As(err, &ae) {
			return c.Status(ae.Status).JSON(ae)
		}
		return c.SendStatus(http.StatusInternalServerError)
	}})
	app.Post("/logout", middleware.RequireAppSession(manager, auth), handler.Logout)
	app.Get("/protected", middleware.RequireAppSession(manager, auth), func(c *fiber.Ctx) error {
		return c.SendStatus(http.StatusNoContent)
	})
	app.Post("/refresh", handler.Refresh)
	makeCookie := func(source models.LoginSource) *http.Cookie {
		sid := ""
		if source == models.LoginSourceSSO {
			sid = "central-family"
		}
		value, encodeErr := manager.Encode(services.AppSessionData{
			AppUserID: handlerTestAppUserID, Role: models.RoleUser, Status: models.UserStatusApproved,
			LoginSource: source, SID: sid, AccessToken: "access", RefreshToken: "refresh",
		})
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		return &http.Cookie{Name: services.AppSessionCookieName, Value: value}
	}
	ssoCookie, directCookie := makeCookie(models.LoginSourceSSO), makeCookie(models.LoginSourceDirect)
	request := func(method, path string, cookie *http.Cookie) *http.Response {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(cookie)
		resp, requestErr := app.Test(req)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	initiation := request(http.MethodPost, "/logout", ssoCookie)
	if initiation.StatusCode != http.StatusOK || len(initiation.Cookies()) != 0 || tokenLogouts.Load() != 0 {
		t.Fatal("SSO initiation destroyed the session before a Central decision")
	}
	// Cancel leaves Central active. The exact original cookie must still work.
	if resp := request(http.MethodGet, "/protected", ssoCookie); resp.StatusCode != http.StatusNoContent {
		t.Fatal("Cancel lost the originating app session")
	}
	beforeDirect := introspections.Load()
	if resp := request(http.MethodPost, "/logout", directCookie); resp.StatusCode != http.StatusOK || len(resp.Cookies()) == 0 {
		t.Fatal("DIRECT logout did not clear its cookie immediately")
	}
	if introspections.Load() != beforeDirect || !active.Load() || tokenLogouts.Load() != 1 {
		t.Fatal("DIRECT logout depended on or revoked the SSO family")
	}
	if resp := request(http.MethodGet, "/protected", ssoCookie); resp.StatusCode != http.StatusNoContent {
		t.Fatal("DIRECT logout invalidated an independent SSO session")
	}

	// Central Confirm revokes the family. Both requests and refresh fail closed.
	active.Store(false)
	for _, target := range []struct{ method, path string }{{http.MethodGet, "/protected"}, {http.MethodPost, "/refresh"}} {
		resp := request(target.method, target.path, ssoCookie)
		if resp.StatusCode != http.StatusUnauthorized || len(resp.Cookies()) == 0 || resp.Cookies()[0].Value != "" {
			t.Fatalf("confirmed revocation did not clear and reject %s", target.path)
		}
	}
	beforeDirect = introspections.Load()
	if resp := request(http.MethodGet, "/protected", makeCookie(models.LoginSourceDirect)); resp.StatusCode != http.StatusNoContent {
		t.Fatal("family revocation invalidated an independent DIRECT session")
	}
	if introspections.Load() != beforeDirect {
		t.Fatal("DIRECT session unexpectedly required SID introspection")
	}
}
