package handlers

import (
	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
)

type ProofHandler struct {
	proofs   *repository.ProofRepository
	bookings *repository.BookingRepository
}

func NewProofHandler(proofs *repository.ProofRepository, bookings *repository.BookingRepository) *ProofHandler {
	return &ProofHandler{proofs: proofs, bookings: bookings}
}

func (h *ProofHandler) canAccessBooking(c *fiber.Ctx, b *models.Booking) bool {
	if middleware.RoleFromCtx(c) == models.RoleAdmin {
		return true
	}
	return b.UserID == middleware.UserIDFromCtx(c)
}

func (h *ProofHandler) List(c *fiber.Ctx) error {
	bookingID := c.Params("bookingId")
	b, err := h.bookings.GetByID(c.Context(), bookingID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load booking")
	}
	if b == nil {
		return fiber.NewError(fiber.StatusNotFound, "booking not found")
	}
	if !h.canAccessBooking(c, b) {
		return fiber.NewError(fiber.StatusForbidden, "you do not have access to this booking")
	}

	items, err := h.proofs.ListByBooking(c.Context(), bookingID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list proofs")
	}
	if items == nil {
		items = []models.BookingProof{}
	}
	return c.JSON(items)
}

func (h *ProofHandler) Create(c *fiber.Ctx) error {
	bookingID := c.Params("bookingId")
	b, err := h.bookings.GetByID(c.Context(), bookingID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load booking")
	}
	if b == nil {
		return fiber.NewError(fiber.StatusNotFound, "booking not found")
	}
	if !h.canAccessBooking(c, b) {
		return fiber.NewError(fiber.StatusForbidden, "you do not have access to this booking")
	}

	var in models.ProofInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if in.Kind != models.ProofKindBefore && in.Kind != models.ProofKindAfter {
		return fiber.NewError(fiber.StatusBadRequest, "kind must be 'before' or 'after'")
	}
	if in.Path == "" {
		return fiber.NewError(fiber.StatusBadRequest, "path is required")
	}

	proof, err := h.proofs.Create(c.Context(), bookingID, in.Kind, in.Path, middleware.UserIDFromCtx(c))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to record proof")
	}
	return c.Status(fiber.StatusCreated).JSON(proof)
}
