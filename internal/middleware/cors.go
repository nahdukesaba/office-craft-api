package middleware

import (
	"strings"

	"office-craft-api/internal/apperror"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

// NewCORS builds the browser-origin policy from the configured allowlist.
// Production deployments must set ALLOWED_ORIGINS explicitly; the config
// fallback contains only the two known SILAPET local-development origins.
func NewCORS(allowedOrigins string) fiber.Handler {
	return cors.New(cors.Config{
		AllowOrigins:     strings.TrimSpace(allowedOrigins),
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization",
		AllowMethods:     "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		AllowCredentials: true,
	})
}

// RequireTrustedBrowserOrigin rejects unsafe browser requests from origins
// outside the same explicit allowlist used by CORS. Requests without Origin
// remain available to trusted server-to-server clients.
func RequireTrustedBrowserOrigin(allowedOrigins string) fiber.Handler {
	allowed := make(map[string]struct{})
	for _, origin := range strings.Split(allowedOrigins, ",") {
		if trimmed := strings.TrimSpace(origin); trimmed != "" {
			allowed[trimmed] = struct{}{}
		}
	}
	return func(c *fiber.Ctx) error {
		switch c.Method() {
		case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
			return c.Next()
		}
		origin := strings.TrimSpace(c.Get("Origin"))
		if origin == "" {
			return c.Next()
		}
		if _, ok := allowed[origin]; !ok {
			return apperror.New(fiber.StatusForbidden, "INVALID_ORIGIN", "request origin is not allowed")
		}
		return c.Next()
	}
}
