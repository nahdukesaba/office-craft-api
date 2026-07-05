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

type notifyRequest struct {
	// Note is an optional admin-supplied extra remark - e.g. flagging that
	// a proof photo looks insufficient, or a component appears missing or
	// damaged in the after-photo. Appended to the templated message for
	// the booking's current status.
	Note string `json:"note"`
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

	var in notifyRequest
	_ = c.BodyParser(&in) // body is optional

	result, nErr := h.svc.Notify(c.Context(), c.Params("id"), in.Note)
	if nErr != nil {
		return nErr
	}
	return c.JSON(result)
}
