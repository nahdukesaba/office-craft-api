package handlers

import (
	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/services"
)

type NotifyHandler struct {
	svc      *services.NotifyService
	bookings *repository.BookingRepository
}

func NewNotifyHandler(svc *services.NotifyService, bookings *repository.BookingRepository) *NotifyHandler {
	return &NotifyHandler{svc: svc, bookings: bookings}
}

func (h *NotifyHandler) Notify(c *fiber.Ctx) error {
	// Reuse the same access rule as other booking sub-resources: owner or admin.
	b, err := h.bookings.GetByID(c.Context(), c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load booking")
	}
	if b != nil && middleware.RoleFromCtx(c) != models.RoleAdmin && b.UserID != middleware.UserIDFromCtx(c) {
		return fiber.NewError(fiber.StatusForbidden, "you do not have access to this booking")
	}

	updated, nErr := h.svc.Notify(c.Context(), c.Params("id"))
	if nErr != nil {
		return nErr
	}
	return c.JSON(fiber.Map{"success": true, "booking": updated})
}
