package handlers

import (
	"context"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

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
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list bookings")
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
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load booking")
	}
	if b == nil {
		return fiber.NewError(fiber.StatusNotFound, "booking not found")
	}
	if !h.canAccess(c, b) {
		return fiber.NewError(fiber.StatusForbidden, "you do not have access to this booking")
	}
	return c.JSON(h.enrich(c.Context(), *b))
}

func (h *BookingHandler) Create(c *fiber.Ctx) error {
	var in models.BookingInput
	if err := c.BodyParser(&in); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "invalid request body")
	}
	if in.ResourceID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "resourceId is required")
	}

	userID := middleware.UserIDFromCtx(c) // userId is always derived from the JWT
	booking, err := h.svc.Create(c.Context(), userID, in)
	if err != nil {
		return mapBookingServiceError(err)
	}
	return c.Status(fiber.StatusCreated).JSON(h.enrich(c.Context(), *booking))
}

func mapBookingServiceError(err error) error {
	switch err {
	case services.ErrInvalidInterval, services.ErrTooLong, services.ErrPastBooking, services.ErrEndBeforeStart:
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	case services.ErrResourceMissing:
		return fiber.NewError(fiber.StatusNotFound, err.Error())
	case services.ErrResourceOffline:
		return fiber.NewError(fiber.StatusConflict, err.Error())
	case services.ErrConflict:
		return fiber.NewError(fiber.StatusConflict, err.Error())
	default:
		return fiber.NewError(fiber.StatusInternalServerError, "failed to create booking")
	}
}

func (h *BookingHandler) transition(c *fiber.Ctx, newStatus string, adminOnly bool, allowedFrom ...string) error {
	b, err := h.bookings.GetByID(c.Context(), c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load booking")
	}
	if b == nil {
		return fiber.NewError(fiber.StatusNotFound, "booking not found")
	}

	isAdmin := middleware.RoleFromCtx(c) == models.RoleAdmin
	if adminOnly && !isAdmin {
		return fiber.NewError(fiber.StatusForbidden, "admin privileges required")
	}
	if !adminOnly && !h.canAccess(c, b) {
		return fiber.NewError(fiber.StatusForbidden, "you do not have access to this booking")
	}

	allowed := false
	for _, s := range allowedFrom {
		if b.Status == s {
			allowed = true
			break
		}
	}
	if !allowed {
		return fiber.NewError(fiber.StatusConflict, "booking status "+b.Status+" cannot transition to "+newStatus)
	}

	updated, err := h.bookings.SetStatus(c.Context(), b.ID, newStatus)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to update booking status")
	}
	return c.JSON(h.enrich(c.Context(), *updated))
}

func (h *BookingHandler) Approve(c *fiber.Ctx) error {
	return h.transition(c, models.BookingStatusApproved, true, models.BookingStatusPending)
}

func (h *BookingHandler) Reject(c *fiber.Ctx) error {
	return h.transition(c, models.BookingStatusRejected, true, models.BookingStatusPending)
}

func (h *BookingHandler) Close(c *fiber.Ctx) error {
	return h.transition(c, models.BookingStatusCompleted, true, models.BookingStatusApproved)
}

func (h *BookingHandler) Cancel(c *fiber.Ctx) error {
	return h.transition(c, models.BookingStatusCancelled, false, models.BookingStatusPending, models.BookingStatusApproved)
}
