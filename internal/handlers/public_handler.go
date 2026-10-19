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
	return c.JSON(h.enrichMany(c.Context(), items, nil))
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
	return c.JSON(h.enrichMany(c.Context(), items, res))
}

func (h *PublicHandler) enrichMany(ctx context.Context, items []models.Booking, preloadedResource *models.Resource) []models.BookingWithDetails {
	detailItems := make([]models.BookingWithDetails, 0, len(items))
	if len(items) == 0 {
		return detailItems
	}

	resourceCache := make(map[string]*models.Resource)
	if preloadedResource != nil && preloadedResource.ID != "" {
		resourceCache[preloadedResource.ID] = preloadedResource
	}

	resourceIDSet := make(map[string]struct{})
	userIDSet := make(map[string]struct{})
	for _, b := range items {
		if b.ResourceID != "" {
			if _, ok := resourceCache[b.ResourceID]; !ok {
				resourceIDSet[b.ResourceID] = struct{}{}
			}
		}
		if b.UserID != "" {
			userIDSet[b.UserID] = struct{}{}
		}
	}

	if len(resourceIDSet) > 0 {
		resIDs := make([]string, 0, len(resourceIDSet))
		for id := range resourceIDSet {
			resIDs = append(resIDs, id)
		}
		if batchRes, err := h.resources.GetByIDs(ctx, resIDs); err == nil {
			for id, r := range batchRes {
				resourceCache[id] = r
			}
		}
	}

	userCache := make(map[string]*models.AppUser)
	if len(userIDSet) > 0 {
		usrIDs := make([]string, 0, len(userIDSet))
		for id := range userIDSet {
			usrIDs = append(usrIDs, id)
		}
		if batchUsr, err := h.users.GetByIDs(ctx, usrIDs); err == nil {
			for id, u := range batchUsr {
				userCache[id] = u
			}
		}
	}

	for _, b := range items {
		out := models.BookingWithDetails{Booking: b}
		if b.ResourceID != "" {
			if res, ok := resourceCache[b.ResourceID]; ok {
				out.Resource = res
			} else if res, err := h.resources.GetByID(ctx, b.ResourceID); err == nil {
				resourceCache[b.ResourceID] = res
				out.Resource = res
			}
		}
		if b.UserID != "" {
			if usr, ok := userCache[b.UserID]; ok {
				out.User = usr
			} else if usr, err := h.users.GetByID(ctx, b.UserID); err == nil {
				userCache[b.UserID] = usr
				out.User = usr
			}
		}
		detailItems = append(detailItems, out)
	}
	return detailItems
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
