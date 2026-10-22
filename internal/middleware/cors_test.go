package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/config"
)

func TestLocalCORSAllowlist(t *testing.T) {
	for _, origin := range []string{
		"http://localhost:8081",
		"http://127.0.0.1:8081",
	} {
		t.Run(origin, func(t *testing.T) {
			app := fiber.New()
			app.Use(NewCORS(config.DefaultAllowedOrigins))
			app.Get("/health", func(c *fiber.Ctx) error {
				return c.SendStatus(fiber.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodOptions, "/health", nil)
			request.Header.Set("Origin", origin)
			request.Header.Set("Access-Control-Request-Method", http.MethodGet)

			response, err := app.Test(request)
			if err != nil {
				t.Fatalf("preflight request: %v", err)
			}
			defer response.Body.Close()

			if response.StatusCode != fiber.StatusNoContent {
				t.Fatalf("expected 204, got %d", response.StatusCode)
			}
			if got := response.Header.Get("Access-Control-Allow-Origin"); got != origin {
				t.Fatalf("expected allow-origin %q, got %q", origin, got)
			}
		})
	}
}

func TestLocalCORSRejectsUnlistedOrigin(t *testing.T) {
	app := fiber.New()
	app.Use(NewCORS(config.DefaultAllowedOrigins))
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodOptions, "/health", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("preflight request: %v", err)
	}
	defer response.Body.Close()

	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected allow-origin for unlisted origin: %q", got)
	}
}
