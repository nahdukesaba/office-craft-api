package handlers

import (
	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
)

type UserHandler struct {
	users *repository.UserRepository
}

func NewUserHandler(users *repository.UserRepository) *UserHandler {
	return &UserHandler{users: users}
}

// List returns all users, optionally filtered by ?status=pending|approved|rejected.
func (h *UserHandler) List(c *fiber.Ctx) error {
	status := c.Query("status")
	if status != "" && status != models.UserStatusPending && status != models.UserStatusApproved && status != models.UserStatusRejected {
		return apperror.BadRequest("VALIDATION_ERROR", "status must be one of: pending, approved, rejected")
	}

	items, err := h.users.List(c.Context(), status)
	if err != nil {
		return apperror.Internal("failed to list users")
	}
	if items == nil {
		items = []models.AppUser{}
	}
	return c.JSON(items)
}

func (h *UserHandler) Approve(c *fiber.Ctx) error {
	user, err := h.users.GetByID(c.Context(), c.Params("id"))
	if err != nil {
		return apperror.Internal("failed to get user")
	}
	if user == nil {
		return apperror.NotFound("USER_NOT_FOUND", "user not found")
	}
	if user.Status != models.UserStatusPending {
		return apperror.BadRequest("INVALID_STATUS", "user status must be pending to approve")
	}
	return h.setStatus(c, models.UserStatusApproved)
}

func (h *UserHandler) Reject(c *fiber.Ctx) error {
	user, err := h.users.GetByID(c.Context(), c.Params("id"))
	if err != nil {
		return apperror.Internal("failed to get user")
	}
	if user == nil {
		return apperror.NotFound("USER_NOT_FOUND", "user not found")
	}
	if user.Status != models.UserStatusPending {
		return apperror.BadRequest("INVALID_STATUS", "user status must be pending to reject")
	}
	return h.setStatus(c, models.UserStatusRejected)
}

func (h *UserHandler) setStatus(c *fiber.Ctx, status string) error {
	user, err := h.users.UpdateStatus(c.Context(), c.Params("id"), status)
	if err != nil {
		return apperror.Internal("failed to update user status")
	}
	if user == nil {
		return apperror.NotFound("USER_NOT_FOUND", "user not found")
	}
	return c.JSON(user)
}
