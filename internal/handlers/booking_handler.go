package handlers

import (
	"context"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/services"
)

type BookingHandler struct {
	bookings  *repository.BookingRepository
	resources *repository.ResourceRepository
	users     *repository.UserRepository
	svc       *services.BookingService
}

func NewBookingHandler(bookings *repository.BookingRepository, resources *repository.ResourceRepository, users *repository.UserRepository, svc *services.BookingService) *BookingHandler {
	return &BookingHandler{bookings: bookings, resources: resources, users: users, svc: svc}
}

func (h *BookingHandler) enrich(ctx context.Context, b models.Booking) models.BookingWithDetails {
	out := models.BookingWithDetails{Booking: b}
	if res, err := h.resources.GetByID(ctx, b.ResourceID); err == nil {
		out.Resource = res
	}
	if usr, err := h.users.GetByID(ctx, b.UserID); err == nil {
		out.User = usr
	}
	return out
}

func parseTimeQuery(v string) *time.Time {
	if v == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return &t
	}
	if t, err := time.Parse("2006-01-02", v); err == nil {
		return &t
	}
	return nil
}

func (h *BookingHandler) List(c *fiber.Ctx) error {
	filter := repository.BookingFilter{
		Status:     c.Query("status"),
		ResourceID: c.Query("resourceId"),
		From:       parseTimeQuery(c.Query("from")),
		To:         parseTimeQuery(c.Query("to")),
	}
	filter.Page, _ = strconv.Atoi(c.Query("page", "1"))
	filter.PageSize, _ = strconv.Atoi(c.Query("pageSize", "20"))

	// Non-admins may only ever see their own bookings, regardless of what
	// userId filter they pass.
	if middleware.RoleFromCtx(c) != models.RoleAdmin {
		filter.UserID = middleware.UserIDFromCtx(c)
	} else {
		filter.UserID = c.Query("userId")
	}

	items, total, err := h.bookings.List(c.Context(), filter)
	if err != nil {
		return apperror.Internal("failed to list bookings")
	}

	enriched := make([]models.BookingWithDetails, 0, len(items))
	for _, b := range items {
		enriched = append(enriched, h.enrich(c.Context(), b))
	}

	pageSize := filter.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	totalPages := int((total + int64(pageSize) - 1) / int64(pageSize))

	return c.JSON(models.PaginatedBookings{
		Data:       enriched,
		Page:       filter.Page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
	})
}

func (h *BookingHandler) canAccess(c *fiber.Ctx, b *models.Booking) bool {
	if middleware.RoleFromCtx(c) == models.RoleAdmin {
		return true
	}
	return b.UserID == middleware.UserIDFromCtx(c)
}

func (h *BookingHandler) Get(c *fiber.Ctx) error {
	b, err := h.bookings.GetByID(c.Context(), c.Params("id"))
	if err != nil {
		return apperror.Internal("failed to load booking")
	}
	if b == nil {
		return apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if !h.canAccess(c, b) {
		return apperror.Forbidden("FORBIDDEN", "you do not have access to this booking")
	}
	return c.JSON(h.enrich(c.Context(), *b))
}

func (h *BookingHandler) Create(c *fiber.Ctx) error {
	var in models.BookingInput
	if err := c.BodyParser(&in); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if in.ResourceID == "" {
		return apperror.BadRequest("VALIDATION_ERROR", "resourceId is required")
	}

	userID := middleware.UserIDFromCtx(c) // userId is always derived from the JWT
	booking, err := h.svc.Create(c.Context(), userID, in)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(h.enrich(c.Context(), *booking))
}

// Approve approves a pending booking, auto-rejecting any other pending
// booking that overlaps the same window.
func (h *BookingHandler) Approve(c *fiber.Ctx) error {
	approved, autoRejectedIDs, err := h.svc.Approve(c.Context(), c.Params("id"))
	if err != nil {
		return err
	}
	if autoRejectedIDs == nil {
		autoRejectedIDs = []string{}
	}
	return c.JSON(models.ApproveBookingResponse{
		Booking:         h.enrich(c.Context(), *approved),
		AutoRejectedIDs: autoRejectedIDs,
	})
}

func (h *BookingHandler) Reject(c *fiber.Ctx) error {
	b, err := h.bookings.GetByID(c.Context(), c.Params("id"))
	if err != nil {
		return apperror.Internal("failed to load booking")
	}
	if b == nil {
		return apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if middleware.RoleFromCtx(c) != models.RoleAdmin {
		return apperror.Forbidden("FORBIDDEN", "admin privileges required")
	}
	updated, err := h.svc.Reject(c.Context(), b.ID)
	if err != nil {
		return err
	}
	return c.JSON(h.enrich(c.Context(), *updated))
}

func (h *BookingHandler) Cancel(c *fiber.Ctx) error {
	b, err := h.bookings.GetByID(c.Context(), c.Params("id"))
	if err != nil {
		return apperror.Internal("failed to load booking")
	}
	if b == nil {
		return apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if !h.canAccess(c, b) {
		return apperror.Forbidden("FORBIDDEN", "you do not have access to this booking")
	}
	updated, err := h.svc.Cancel(c.Context(), b.ID)
	if err != nil {
		return err
	}
	return c.JSON(h.enrich(c.Context(), *updated))
}

// Revoke is an admin-only override that cancels an approved/in_use booking.
func (h *BookingHandler) Revoke(c *fiber.Ctx) error {
	var in models.RevokeInput
	_ = c.BodyParser(&in) // body is optional

	updated, err := h.svc.Revoke(c.Context(), c.Params("id"), in.AdminNotes, in.Reason)
	if err != nil {
		return err
	}

	// Best-effort notification to the owner; failures here shouldn't block
	// the revoke itself from succeeding.
	return c.JSON(h.enrich(c.Context(), *updated))
}

// Start transitions an approved booking to in_use.
func (h *BookingHandler) Start(c *fiber.Ctx) error {
	b, err := h.bookings.GetByID(c.Context(), c.Params("id"))
	if err != nil {
		return apperror.Internal("failed to load booking")
	}
	if b == nil {
		return apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if !h.canAccess(c, b) {
		return apperror.Forbidden("FORBIDDEN", "you do not have access to this booking")
	}
	updated, err := h.svc.Start(c.Context(), b.ID)
	if err != nil {
		return err
	}
	return c.JSON(h.enrich(c.Context(), *updated))
}

// Finish transitions an in_use booking to finished.
func (h *BookingHandler) Finish(c *fiber.Ctx) error {
	b, err := h.bookings.GetByID(c.Context(), c.Params("id"))
	if err != nil {
		return apperror.Internal("failed to load booking")
	}
	if b == nil {
		return apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if !h.canAccess(c, b) {
		return apperror.Forbidden("FORBIDDEN", "you do not have access to this booking")
	}
	updated, err := h.svc.Finish(c.Context(), b.ID)
	if err != nil {
		return err
	}
	return c.JSON(h.enrich(c.Context(), *updated))
}
