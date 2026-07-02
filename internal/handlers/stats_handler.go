package handlers

import (
	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/repository"
)

type StatsHandler struct {
	bookings  *repository.BookingRepository
	resources *repository.ResourceRepository
	users     *repository.UserRepository
}

func NewStatsHandler(bookings *repository.BookingRepository, resources *repository.ResourceRepository, users *repository.UserRepository) *StatsHandler {
	return &StatsHandler{bookings: bookings, resources: resources, users: users}
}

type overviewResponse struct {
	TotalResources    int64            `json:"totalResources"`
	TotalUsers        int64            `json:"totalUsers"`
	TotalBookings     int64            `json:"totalBookings"`
	BookingsByStatus  map[string]int64 `json:"bookingsByStatus"`
	PendingBookings   int64            `json:"pendingBookings"`
	ApprovedBookings  int64            `json:"approvedBookings"`
	CompletedBookings int64            `json:"completedBookings"`
}

func (h *StatsHandler) Overview(c *fiber.Ctx) error {
	resourceCount, err := h.resources.Count(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load resource stats")
	}
	userCount, err := h.users.Count(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load user stats")
	}
	byStatus, err := h.bookings.CountByStatus(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to load booking stats")
	}

	var total int64
	for _, v := range byStatus {
		total += v
	}

	return c.JSON(overviewResponse{
		TotalResources:    resourceCount,
		TotalUsers:        userCount,
		TotalBookings:     total,
		BookingsByStatus:  byStatus,
		PendingBookings:   byStatus["pending"],
		ApprovedBookings:  byStatus["approved"],
		CompletedBookings: byStatus["completed"],
	})
}
