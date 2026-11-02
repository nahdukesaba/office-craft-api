package middleware

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
	"office-craft-api/internal/services"
)

const LocalsAppSession = "appSession"

type centralSSOSessionIntrospector interface {
	IntrospectBrowserSSO(context.Context, string, string) (bool, error)
}

// AppSessionCookieOrNoContent lets /auth/me act as a quiet session probe for
// signed-out browsers. A present cookie still flows through the full session,
// SSO introspection, token, and membership validation chain.
func AppSessionCookieOrNoContent(c *fiber.Ctx) error {
	if c.Cookies(services.AppSessionCookieName) == "" {
		return c.SendStatus(fiber.StatusNoContent)
	}
	return c.Next()
}

func RequireAppSession(
	manager *services.AppSessionManager,
	introspectors ...centralSSOSessionIntrospector,
) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if manager == nil {
			return apperror.New(fiber.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "application session is unavailable")
		}
		data, err := manager.Read(c)
		if err != nil {
			manager.Clear(c)
			return apperror.Unauthorized("invalid or expired application session")
		}
		if data.LoginSource == models.LoginSourceSSO {
			if len(introspectors) == 0 || introspectors[0] == nil {
				manager.Clear(c)
				return apperror.Unauthorized("SSO session validation is unavailable")
			}
			active, introspectionErr := introspectors[0].IntrospectBrowserSSO(
				c.UserContext(), data.SID, data.AppUserID,
			)
			if introspectionErr != nil || !active {
				manager.Clear(c)
				return apperror.Unauthorized("SSO session is invalid or expired")
			}
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
