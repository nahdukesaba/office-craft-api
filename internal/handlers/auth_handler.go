package handlers

import (
	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/services"
)

type AuthHandler struct {
	auth  *services.AuthService
	users *repository.UserRepository
}

func NewAuthHandler(auth *services.AuthService, users *repository.UserRepository) *AuthHandler {
	return &AuthHandler{auth: auth, users: users}
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
