package handlers

import (
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
}

func NewPublicHandler(bookings *repository.BookingRepository, resources *repository.ResourceRepository) *PublicHandler {
	return &PublicHandler{bookings: bookings, resources: resources}
}

type publicBooking struct {
	ID         string `json:"id"`
	ResourceID string `json:"resourceId"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
	Status     string `json:"status"`
}

func toPublicBookings(items []models.Booking) []publicBooking {
	out := make([]publicBooking, 0, len(items))
	for _, b := range items {
		out = append(out, publicBooking{
			ID:         b.ID,
			ResourceID: b.ResourceID,
			StartTime:  b.StartTime.Format("2006-01-02T15:04:05Z07:00"),
			EndTime:    b.EndTime.Format("2006-01-02T15:04:05Z07:00"),
			Status:     b.Status,
		})
	}
	return out
}

func (h *PublicHandler) AllBookings(c *fiber.Ctx) error {
	items, err := h.bookings.ListPublic(c.Context(), "")
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list bookings")
	}
	return c.JSON(toPublicBookings(items))
}

func (h *PublicHandler) BookingsForResource(c *fiber.Ctx) error {
	resourceID := c.Params("resourceId")
	res, err := h.resources.GetByID(c.Context(), resourceID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load resource")
	}
	if res == nil {
		return fiber.NewError(fiber.StatusNotFound, "resource not found")
	}

	items, err := h.bookings.ListPublic(c.Context(), resourceID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to list bookings")
	}
	return c.JSON(toPublicBookings(items))
}
