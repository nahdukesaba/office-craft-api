package handlers

import (
	"github.com/gofiber/fiber/v2"

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
}

type authResponse struct {
	Token string      `json:"token"`
	User  interface{} `json:"user"`
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if req.Email == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "email and password are required")
	}

	token, user, err := h.auth.Login(c.Context(), req.Email, req.Password)
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return fiber.NewError(ae.StatusCode, ae.Message)
		}
		return fiber.NewError(fiber.StatusInternalServerError, "login failed")
	}

	return c.JSON(authResponse{Token: token, User: user})
}

func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if req.Email == "" || req.Password == "" || req.FullName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "email, password and fullName are required")
	}

	token, user, err := h.auth.Register(c.Context(), req.Email, req.Password, req.FullName)
	if err != nil {
		if ae, ok := err.(*services.AuthError); ok {
			return fiber.NewError(ae.StatusCode, ae.Message)
		}
		return fiber.NewError(fiber.StatusInternalServerError, "registration failed")
	}

	if token == "" {
		// Supabase project has "confirm email" enabled: no session yet.
		return c.Status(fiber.StatusCreated).JSON(fiber.Map{
			"message": "account created, please confirm your email before logging in",
			"user":    user,
		})
	}

	return c.Status(fiber.StatusCreated).JSON(authResponse{Token: token, User: user})
}

func (h *AuthHandler) Me(c *fiber.Ctx) error {
	userID := middleware.UserIDFromCtx(c)
	user, err := h.users.GetByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load current user")
	}
	if user == nil {
		return fiber.NewError(fiber.StatusNotFound, "user not found")
	}
	return c.JSON(user)
}
