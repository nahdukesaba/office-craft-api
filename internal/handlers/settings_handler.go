package handlers

import (
	"log"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
)

type SettingsHandler struct {
	rules *repository.BookingRulesRepository
}

func NewSettingsHandler(rules *repository.BookingRulesRepository) *SettingsHandler {
	return &SettingsHandler{rules: rules}
}

func (h *SettingsHandler) GetBookingRules(c *fiber.Ctx) error {
	rules, err := h.rules.Get(c.Context())
	if err != nil {
		log.Printf("settings.GetBookingRules: %v", err)
		return apperror.Internal("failed to load booking rules")
	}
	return c.JSON(rules)
}

func (h *SettingsHandler) UpdateBookingRules(c *fiber.Ctx) error {
	var in models.BookingRulesInput
	if err := c.BodyParser(&in); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if in.BlockResourceOpenUsage == nil || in.BlockUserUnfinished == nil {
		return apperror.BadRequest("VALIDATION_ERROR", "blockResourceOpenUsage and blockUserUnfinished are required")
	}

	rules := models.BookingRules{
		BlockResourceOpenUsage: *in.BlockResourceOpenUsage,
		BlockUserUnfinished:    *in.BlockUserUnfinished,
	}
	if err := h.rules.Save(c.Context(), rules, middleware.UserIDFromCtx(c)); err != nil {
		log.Printf("settings.UpdateBookingRules: %v", err)
		return apperror.Internal("failed to save booking rules")
	}
	return c.JSON(rules)
}
