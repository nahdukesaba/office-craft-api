package handlers

import (
	"context"
	"strings"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
	"office-craft-api/internal/services"
)

type authHandlerUserStore interface {
	GetByID(ctx context.Context, id string) (*models.AppUser, error)
}

type AuthHandler struct {
	auth        *services.AuthService
	users       authHandlerUserStore
	appSessions *services.AppSessionManager
}

func NewAuthHandler(
	auth *services.AuthService,
	users authHandlerUserStore,
	appSessions ...*services.AppSessionManager,
) *AuthHandler {
	handler := &AuthHandler{
		auth:  auth,
		users: users,
	}
	if len(appSessions) > 0 {
		handler.appSessions = appSessions[0]
	}
	return handler
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"fullName"`
	Phone    string `json:"phone"`
}

type authResponse struct {
	User interface{} `json:"user"`
}

type browserSSOExchangeRequest struct {
	Code   string `json:"code"`
	State  string `json:"state"`
	Status string `json:"status"`
}

func (h *AuthHandler) SSOStart(c *fiber.Ctx) error {
	authorizeURL, err := h.auth.StartBrowserSSO(c.UserContext())
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(ae.StatusCode, ae.Code, ae.Message)
		}
		return apperror.Internal("failed to start browser SSO")
	}
	return c.Redirect(authorizeURL, fiber.StatusFound)
}

func (h *AuthHandler) SSOLogoutStart(c *fiber.Ctx) error {
	logoutURL, err := h.auth.StartBrowserSSOLogout()
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(ae.StatusCode, ae.Code, ae.Message)
		}
		return apperror.Internal("failed to start coordinated logout")
	}
	return c.Redirect(logoutURL, fiber.StatusFound)
}

func (h *AuthHandler) SSOExchange(c *fiber.Ctx) error {
	var req browserSSOExchangeRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	result, err := h.auth.CompleteBrowserSSO(
		c.UserContext(), req.State, req.Code, req.Status,
	)
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(ae.StatusCode, ae.Code, ae.Message)
		}
		return apperror.Internal("browser SSO exchange failed")
	}
	if result == nil {
		return apperror.Internal("browser SSO returned no result")
	}
	if result.Session == nil {
		return c.JSON(fiber.Map{"status": result.Status})
	}
	if err := h.setAppSession(c, result.Session, models.LoginSourceSSO); err != nil {
		return apperror.Internal("failed to create application session")
	}
	return c.JSON(authResponse{User: result.Session.User})
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if req.Email == "" || req.Password == "" {
		return apperror.BadRequest("VALIDATION_ERROR", "email and password are required")
	}

	session, err := h.auth.Login(c.Context(), req.Email, req.Password)
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(ae.StatusCode, ae.Code, ae.Message)
		}
		return apperror.Internal("login failed")
	}

	if err := h.setAppSession(c, session, models.LoginSourceDirect); err != nil {
		return apperror.Internal("failed to create application session")
	}
	return c.JSON(authResponse{User: session.User})
}

func (h *AuthHandler) Refresh(c *fiber.Ctx) error {
	if h.appSessions == nil {
		return apperror.New(fiber.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "application session is unavailable")
	}
	current, err := h.appSessions.Read(c)
	if err != nil {
		h.appSessions.Clear(c)
		return apperror.Unauthorized("invalid or expired application session")
	}
	if current.LoginSource == models.LoginSourceSSO {
		active, introspectionErr := h.auth.IntrospectBrowserSSO(
			c.UserContext(), current.SID, current.AppUserID,
		)
		if introspectionErr != nil || !active {
			h.appSessions.Clear(c)
			return apperror.Unauthorized("SSO session is invalid or expired")
		}
	}

	session, err := h.auth.Refresh(
		c.Context(),
		current.RefreshToken,
	)
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(
				ae.StatusCode,
				ae.Code,
				ae.Message,
			)
		}

		return apperror.Internal("refresh failed")
	}

	session.SID = current.SID
	if err := h.setAppSession(c, session, current.LoginSource); err != nil {
		return apperror.Internal("failed to rotate application session")
	}
	return c.JSON(authResponse{User: session.User})
}

func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	session, ok := middleware.AppSessionFromCtx(c)
	if !ok {
		return apperror.Unauthorized("application session is unavailable")
	}
	if session.LoginSource == models.LoginSourceSSO {
		// Preserve the app cookie and upstream session until Central confirms.
		// Cancel returns through /login and validates the same SID; Confirm
		// revokes it and the next /me or refresh clears the invalid app cookie.
		c.Set(fiber.HeaderCacheControl, "no-store")
		return c.JSON(fiber.Map{"centralLogoutRequired": true})
	}
	defer h.appSessions.Clear(c)

	result, err := h.auth.Logout(
		c.Context(),
		session.AccessToken,
		session.LoginSource,
	)
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(
				ae.StatusCode,
				ae.Code,
				ae.Message,
			)
		}

		return apperror.Internal("logout failed")
	}

	middleware.InvalidateCentralSSOAuthCache()
	return c.JSON(fiber.Map{
		"centralLogoutRequired": result.CentralLogoutRequired,
	})
}

func (h *AuthHandler) setAppSession(c *fiber.Ctx, session *services.AuthSession, source models.LoginSource) error {
	if h.appSessions == nil || session == nil || session.User == nil {
		return services.ErrInvalidAppSession
	}
	sid := ""
	if source == models.LoginSourceSSO {
		sid = strings.TrimSpace(session.SID)
	}
	return h.appSessions.Set(c, services.AppSessionData{
		AppUserID: session.User.ID, Role: session.User.Role, Status: session.User.Status,
		LoginSource: source, SID: sid, AccessToken: session.AccessToken, RefreshToken: session.RefreshToken,
	})
}

func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if req.Email == "" || req.Password == "" || req.FullName == "" {
		return apperror.BadRequest("VALIDATION_ERROR", "email, password and fullName are required")
	}

	user, err := h.auth.Register(c.Context(), req.Email, req.Password, req.FullName, req.Phone)
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(ae.StatusCode, ae.Code, ae.Message)
		}
		return apperror.Internal("registration failed")
	}

	// No token is ever issued at registration - every new account needs an
	// admin to approve it first (see PUT /users/:id/approve).
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"message": "account created and awaiting admin approval",
		"user":    user,
	})
}

func (h *AuthHandler) RequestAccess(c *fiber.Ctx) error {
	var req loginRequest

	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest(
			"INVALID_BODY",
			"invalid request body",
		)
	}

	if strings.TrimSpace(req.Email) == "" ||
		req.Password == "" {

		return apperror.BadRequest(
			"VALIDATION_ERROR",
			"email and password are required",
		)
	}

	result, err := h.auth.RequestAccess(
		c.Context(),
		req.Email,
		req.Password,
	)
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(
				ae.StatusCode,
				ae.Code,
				ae.Message,
			)
		}

		return apperror.Internal(
			"failed to request SILAPET access",
		)
	}

	status := fiber.StatusOK
	message := "SILAPET access request is already pending"

	if result.Created {
		status = fiber.StatusCreated
		message = "SILAPET access request submitted"
	}

	return c.Status(status).JSON(fiber.Map{
		"message":     message,
		"user":        result.User,
		"application": result.Application,
		"status":      result.Status,
		"created":     result.Created,
	})
}

func (h *AuthHandler) Me(c *fiber.Ctx) error {
	userID := middleware.UserIDFromCtx(c)
	user, err := h.users.GetByID(c.Context(), userID)
	if err != nil {
		return apperror.Internal("failed to load current user")
	}
	if user == nil {
		return apperror.NotFound("USER_NOT_FOUND", "user not found")
	}
	return c.JSON(user)
}

type changePasswordRequest struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	TokenHash string `json:"tokenHash"`
	Password  string `json:"password"`
}

func (h *AuthHandler) ChangePassword(c *fiber.Ctx) error {
	var req changePasswordRequest

	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest(
			"INVALID_BODY",
			"invalid request body",
		)
	}

	if req.OldPassword == "" ||
		req.NewPassword == "" {

		return apperror.BadRequest(
			"VALIDATION_ERROR",
			"oldPassword and newPassword are required",
		)
	}

	if len(req.NewPassword) < 8 ||
		len(req.NewPassword) > 128 {

		return apperror.BadRequest(
			"VALIDATION_ERROR",
			"newPassword must be between 8 and 128 characters",
		)
	}

	header := strings.TrimSpace(c.Get("Authorization"))
	parts := strings.Fields(header)

	if len(parts) != 2 ||
		!strings.EqualFold(parts[0], "Bearer") ||
		strings.TrimSpace(parts[1]) == "" {

		return apperror.New(
			fiber.StatusUnauthorized,
			"INVALID_TOKEN",
			"invalid access token format",
		)
	}

	if err := h.auth.ChangePassword(
		c.Context(),
		parts[1],
		req.OldPassword,
		req.NewPassword,
	); err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(
				ae.StatusCode,
				ae.Code,
				ae.Message,
			)
		}

		return apperror.Internal(
			"failed to change password",
		)
	}

	middleware.InvalidateCentralSSOAuthCache()
	return c.JSON(
		fiber.Map{"message": "password updated"},
	)
}

func (h *AuthHandler) ForgotPassword(c *fiber.Ctx) error {
	var req forgotPasswordRequest

	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest(
			"INVALID_BODY",
			"invalid request body",
		)
	}

	req.Email = strings.TrimSpace(req.Email)

	if req.Email == "" {
		return apperror.BadRequest(
			"VALIDATION_ERROR",
			"email is required",
		)
	}

	if err := h.auth.ForgotPassword(
		c.Context(),
		req.Email,
	); err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(
				ae.StatusCode,
				ae.Code,
				ae.Message,
			)
		}

		return apperror.Internal(
			"failed to request password recovery",
		)
	}

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"message": "Jika email terdaftar, instruksi reset password akan dikirim.",
	})
}

func (h *AuthHandler) ResetPassword(c *fiber.Ctx) error {
	var req resetPasswordRequest

	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest(
			"INVALID_BODY",
			"invalid request body",
		)
	}

	req.TokenHash = strings.TrimSpace(req.TokenHash)

	if req.TokenHash == "" {
		return apperror.New(
			fiber.StatusUnauthorized,
			"INVALID_RECOVERY_TOKEN",
			"recovery token hash is required",
		)
	}

	if len(req.Password) < 8 ||
		len(req.Password) > 128 {

		return apperror.BadRequest(
			"VALIDATION_ERROR",
			"password must be between 8 and 128 characters",
		)
	}

	if err := h.auth.ResetPassword(
		c.Context(),
		req.TokenHash,
		req.Password,
	); err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(
				ae.StatusCode,
				ae.Code,
				ae.Message,
			)
		}

		return apperror.Internal(
			"failed to reset password",
		)
	}

	middleware.InvalidateCentralSSOAuthCache()
	return c.SendStatus(fiber.StatusNoContent)
}

type changePhoneNumberRequest struct {
	Phone string `json:"phone"`
}

func (h *AuthHandler) ChangePhoneNumber(c *fiber.Ctx) error {
	var req changePhoneNumberRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if req.Phone == "" {
		return apperror.BadRequest("VALIDATION_ERROR", "Phone number are required")
	}

	userID := middleware.UserIDFromCtx(c)
	user, err := h.users.GetByID(c.Context(), userID)
	if err != nil {
		return apperror.Internal("failed to load current user")
	}
	if user == nil {
		return apperror.NotFound("USER_NOT_FOUND", "user not found")
	}

	if err := h.auth.ChangePhoneNumber(c.Context(), user, req.Phone); err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(ae.StatusCode, ae.Code, ae.Message)
		}
		return apperror.Internal("failed to change phone number")
	}

	return c.JSON(fiber.Map{"message": "phone number updated"})
}
