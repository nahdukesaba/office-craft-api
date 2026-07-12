package handlers

import (
	"context"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
)

// PublicHandler exposes read-only, unauthenticated endpoints used by the
// frontend's public calendar / availability views. Only the fields needed
// for those views are returned - no user PII.
type PublicHandler struct {
	bookings  *repository.BookingRepository
	resources *repository.ResourceRepository
	users     *repository.UserRepository
}

func NewPublicHandler(bookings *repository.BookingRepository, resources *repository.ResourceRepository, users *repository.UserRepository) *PublicHandler {
	return &PublicHandler{bookings: bookings, resources: resources, users: users}
}

type publicBooking struct {
	ID         string  `json:"id"`
	ResourceID string  `json:"resourceId"`
	PhotoURL   *string `json:"photoUrl"`
	StartTime  string  `json:"startTime"`
	EndTime    string  `json:"endTime"`
	Status     string  `json:"status"`
}

func toPublicBookings(items []models.Booking, photoURL *string) []publicBooking {
	out := make([]publicBooking, 0, len(items))
	for _, b := range items {
		out = append(out, publicBooking{
			ID:         b.ID,
			ResourceID: b.ResourceID,
			StartTime:  b.StartTime.Format("2006-01-02T15:04:05Z07:00"),
			EndTime:    b.EndTime.Format("2006-01-02T15:04:05Z07:00"),
			Status:     b.Status,
			PhotoURL:   photoURL,
		})
	}
	return out
}

func (h *PublicHandler) AllBookings(c *fiber.Ctx) error {
	month := c.Query("month", "0")
	monthInt, _ := strconv.Atoi(month)
	year := c.Query("year", "0")
	yearInt, _ := strconv.Atoi(year)
	items, err := h.bookings.ListPublic(c.Context(), "", monthInt, yearInt)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list bookings")
	}
	detailItems := make([]models.BookingWithDetails, 0, len(items))
	for _, item := range items {
		detailItems = append(detailItems, h.enrich(c.Context(), item))
	}
	return c.JSON(detailItems)
}

func (h *PublicHandler) BookingsForResource(c *fiber.Ctx) error {
	resourceID := c.Params("resourceId")
	monthInt, _ := strconv.Atoi(c.Query("month", "0"))
	yearInt, _ := strconv.Atoi(c.Query("year", "0"))
	res, err := h.resources.GetByID(c.Context(), resourceID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load resource")
	}
	if res == nil {
		return fiber.NewError(fiber.StatusNotFound, "resource not found")
	}

	items, err := h.bookings.ListPublic(c.Context(), resourceID, monthInt, yearInt)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list bookings")
	}
	detailItems := make([]models.BookingWithDetails, 0, len(items))
	for _, item := range items {
		detailItems = append(detailItems, h.enrich(c.Context(), item))
	}
	return c.JSON(detailItems)
}

func (h *PublicHandler) enrich(ctx context.Context, b models.Booking) models.BookingWithDetails {
	out := models.BookingWithDetails{Booking: b}
	if res, err := h.resources.GetByID(ctx, b.ResourceID); err == nil {
		out.Resource = res
	}
	if usr, err := h.users.GetByID(ctx, b.UserID); err == nil {
		out.User = usr
	}
	return out
}
