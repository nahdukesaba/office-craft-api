package handlers

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/services"
)

type AuthHandler struct {
	auth  *services.AuthService
	users *repository.UserRepository
	email *services.EmailService
}

func NewAuthHandler(auth *services.AuthService, users *repository.UserRepository, email *services.EmailService) *AuthHandler {
	return &AuthHandler{auth: auth, users: users, email: email}
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
	Token string      `json:"token"`
	User  interface{} `json:"user"`
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if req.Email == "" || req.Password == "" {
		return apperror.BadRequest("VALIDATION_ERROR", "email and password are required")
	}

	token, user, err := h.auth.Login(c.Context(), req.Email, req.Password)
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(ae.StatusCode, ae.Code, ae.Message)
		}
		return apperror.Internal("login failed")
	}

	return c.JSON(authResponse{Token: token, User: user})
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

// ChangePassword is the self-service "update my password" endpoint -
// requires the caller's current password to confirm it's really them,
// separate from just holding a valid bearer token.
func (h *AuthHandler) ChangePassword(c *fiber.Ctx) error {
	var req changePasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if req.OldPassword == "" || req.NewPassword == "" {
		return apperror.BadRequest("VALIDATION_ERROR", "oldPassword and newPassword are required")
	}
	if len(req.NewPassword) < 8 {
		return apperror.BadRequest("VALIDATION_ERROR", "newPassword must be at least 8 characters")
	}

	userID := middleware.UserIDFromCtx(c)
	user, err := h.users.GetByID(c.Context(), userID)
	if err != nil {
		return apperror.Internal("failed to load current user")
	}
	if user == nil {
		return apperror.NotFound("USER_NOT_FOUND", "user not found")
	}

	// The password-change call to Supabase needs the caller's own access
	// token (not our derived session data), so pull it straight off the
	// same header RequireAuth already validated.
	token := strings.TrimPrefix(c.Get("Authorization"), "Bearer ")

	if err := h.auth.ChangePassword(c.Context(), token, user.Email, req.OldPassword, req.NewPassword); err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(ae.StatusCode, ae.Code, ae.Message)
		}
		return apperror.Internal("failed to change password")
	}

	return c.JSON(fiber.Map{"message": "password updated"})
}

type adminResetPasswordRequest struct {
	UserID string `json:"userId"`
}

// AdminResetPassword lets an admin force-reset another user's password to
// a freshly generated random one. The new password is returned in the
// response (so the admin can relay it even if email isn't configured) and
// is also best-effort emailed to the user directly.
func (h *AuthHandler) AdminResetPassword(c *fiber.Ctx) error {
	var req adminResetPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if req.UserID == "" {
		return apperror.BadRequest("VALIDATION_ERROR", "userId is required")
	}

	target, err := h.users.GetByID(c.Context(), req.UserID)
	if err != nil {
		return apperror.Internal("failed to load target user")
	}
	if target == nil {
		return apperror.NotFound("USER_NOT_FOUND", "user not found")
	}

	newPassword, err := h.auth.AdminResetPassword(c.Context(), req.UserID)
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return apperror.New(ae.StatusCode, ae.Code, ae.Message)
		}
		return apperror.Internal(err.Error())
	}

	emailSent := false
	subject := "Your Office-Craft password was reset"
	body := fmt.Sprintf(
		"<p>Hi %s,</p><p>An admin has reset your Office-Craft password. Your new temporary password is:</p><p><b>%s</b></p><p>Please log in and change it as soon as possible.</p>",
		target.FullName, newPassword,
	)
	if h.email.Send(target.Email, target.FullName, subject, body) == nil {
		emailSent = true
	}

	return c.JSON(fiber.Map{
		"message":           "password reset",
		"temporaryPassword": newPassword,
		"emailSent":         emailSent,
	})
}
