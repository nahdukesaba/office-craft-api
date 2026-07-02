package middleware

import (
	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/models"
)

// RequireAdmin must run after RequireAuth. It rejects any request whose
// resolved app role is not "admin".
func RequireAdmin() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if RoleFromCtx(c) != models.RoleAdmin {
			return fiber.NewError(fiber.StatusForbidden, "admin privileges required")
		}
		return c.Next()
	}
}
