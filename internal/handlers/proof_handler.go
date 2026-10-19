package handlers

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
)

var safeProofFilenamePattern = regexp.MustCompile(`^(before|after)-[a-zA-Z0-9_-]{4,64}\.(jpg|jpeg|png|webp|gif)$`)

type ProofHandler struct {
	proofs   storageProofStore
	bookings storageBookingFinder
	svc      storageProofValidator
}

func NewProofHandler(proofs storageProofStore, bookings storageBookingFinder, svc storageProofValidator) *ProofHandler {
	return &ProofHandler{proofs: proofs, bookings: bookings, svc: svc}
}

func (h *ProofHandler) canAccessBooking(c *fiber.Ctx, b *models.Booking) bool {
	if middleware.RoleFromCtx(c) == models.RoleAdmin {
		return true
	}
	return b.UserID == middleware.UserIDFromCtx(c)
}

func validateLegacyProofPath(b *models.Booking, actorID, kind, rawPath string) (string, error) {
	cleaned := strings.TrimSpace(rawPath)
	if cleaned == "" {
		return "", apperror.BadRequest("VALIDATION_ERROR", "path is required")
	}

	if strings.HasPrefix(cleaned, "/") ||
		strings.HasSuffix(cleaned, "/") ||
		strings.ContainsAny(cleaned, "\\?#\x00") ||
		strings.HasPrefix(cleaned, "http://") ||
		strings.HasPrefix(cleaned, "https://") {
		return "", apperror.BadRequest("INVALID_PROOF_PATH", "invalid proof object path")
	}

	parts := strings.Split(cleaned, "/")
	if len(parts) != 3 {
		return "", apperror.BadRequest("INVALID_PROOF_PATH", "proof path must match {userId}/{bookingId}/{kind}-{id}.{ext}")
	}

	ownerSegment := strings.TrimSpace(parts[0])
	bookingSegment := strings.TrimSpace(parts[1])
	fileSegment := strings.TrimSpace(parts[2])

	if ownerSegment == "" || ownerSegment == "." || ownerSegment == ".." ||
		bookingSegment == "" || bookingSegment == "." || bookingSegment == ".." ||
		fileSegment == "" || fileSegment == "." || fileSegment == ".." {
		return "", apperror.BadRequest("INVALID_PROOF_PATH", "invalid proof object path")
	}

	if bookingSegment != strings.TrimSpace(b.ID) {
		return "", apperror.BadRequest("INVALID_PROOF_PATH", "proof path does not match booking ID")
	}

	if ownerSegment != strings.TrimSpace(b.UserID) && ownerSegment != strings.TrimSpace(actorID) {
		return "", apperror.BadRequest("INVALID_PROOF_PATH", "proof path does not match booking owner")
	}

	lowerFile := strings.ToLower(fileSegment)
	if !strings.HasPrefix(lowerFile, kind+"-") || !safeProofFilenamePattern.MatchString(lowerFile) {
		return "", apperror.BadRequest("INVALID_PROOF_PATH", "proof filename must match kind and allowed image extension")
	}

	ext := strings.ToLower(filepath.Ext(fileSegment))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
	default:
		return "", apperror.BadRequest("INVALID_PROOF_PATH", "unsupported proof image extension")
	}

	return cleaned, nil
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

	actorID := middleware.UserIDFromCtx(c)
	validatedPath, err := validateLegacyProofPath(b, actorID, in.Kind, in.Path)
	if err != nil {
		return err
	}

	// Gating rules: before/after are only allowed within specific
	// status + date windows - see ProofService for the exact rules.
	if _, err := h.svc.ValidateUpload(c.Context(), bookingID, in.Kind); err != nil {
		return err
	}

	proof, err := h.proofs.Create(c.Context(), bookingID, in.Kind, validatedPath, actorID)
	if err != nil {
		return apperror.Internal("failed to record proof")
	}
	return c.Status(fiber.StatusCreated).JSON(proof)
}
