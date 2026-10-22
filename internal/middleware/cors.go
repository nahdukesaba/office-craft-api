package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

// NewCORS builds the browser-origin policy from the configured allowlist.
// Production deployments must set ALLOWED_ORIGINS explicitly; the config
// fallback contains only the two known SILAPET local-development origins.
func NewCORS(allowedOrigins string) fiber.Handler {
	return cors.New(cors.Config{
		AllowOrigins: strings.TrimSpace(allowedOrigins),
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, PUT, PATCH, DELETE, OPTIONS",
	})
}
