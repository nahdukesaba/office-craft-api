package middleware

import (
	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/services"
)

const LocalsAppSession = "appSession"

func RequireAppSession(manager *services.AppSessionManager) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if manager == nil {
			return apperror.New(fiber.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "application session is unavailable")
		}
		data, err := manager.Read(c)
		if err != nil {
			manager.Clear(c)
			return apperror.Unauthorized("invalid or expired application session")
		}
		c.Locals(LocalsAppSession, data)
		c.Request().Header.Set("Authorization", "Bearer "+data.AccessToken)
		return c.Next()
	}
}

func AppSessionFromCtx(c *fiber.Ctx) (*services.AppSessionData, bool) {
	data, ok := c.Locals(LocalsAppSession).(*services.AppSessionData)
	return data, ok && data != nil
}
