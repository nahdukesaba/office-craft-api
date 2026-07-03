package handlers

import (
	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/services"
)

type ProofHandler struct {
	proofs   *repository.ProofRepository
	bookings *repository.BookingRepository
	svc      *services.ProofService
}

func NewProofHandler(proofs *repository.ProofRepository, bookings *repository.BookingRepository, svc *services.ProofService) *ProofHandler {
	return &ProofHandler{proofs: proofs, bookings: bookings, svc: svc}
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
		return apperror.Internal("failed to load booking")
	}
	if b == nil {
		return apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if !h.canAccessBooking(c, b) {
		return apperror.Forbidden("FORBIDDEN", "you do not have access to this booking")
	}

	items, err := h.proofs.ListByBooking(c.Context(), bookingID)
	if err != nil {
		return apperror.Internal("failed to list proofs")
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
		return apperror.Internal("failed to load booking")
	}
	if b == nil {
		return apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if !h.canAccessBooking(c, b) {
		return apperror.Forbidden("FORBIDDEN", "you do not have access to this booking")
	}

	var in models.ProofInput
	if err := c.BodyParser(&in); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if in.Kind != models.ProofKindBefore && in.Kind != models.ProofKindAfter {
		return apperror.BadRequest("INVALID_KIND", "kind must be 'before' or 'after'")
	}
	if in.Path == "" {
		return apperror.BadRequest("VALIDATION_ERROR", "path is required")
	}

	// Gating rules: before/after are only allowed within specific
	// status + date windows - see ProofService for the exact rules.
	if _, err := h.svc.ValidateUpload(c.Context(), bookingID, in.Kind); err != nil {
		return err
	}

	proof, err := h.proofs.Create(c.Context(), bookingID, in.Kind, in.Path, middleware.UserIDFromCtx(c))
	if err != nil {
		return apperror.Internal("failed to record proof")
	}
	return c.Status(fiber.StatusCreated).JSON(proof)
}
