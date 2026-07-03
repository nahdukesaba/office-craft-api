package handlers

import (
	"log"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
)

type ResourceHandler struct {
	resources *repository.ResourceRepository
}

func NewResourceHandler(resources *repository.ResourceRepository) *ResourceHandler {
	return &ResourceHandler{resources: resources}
}

func (h *ResourceHandler) List(c *fiber.Ctx) error {
	filter := repository.ResourceFilter{
		Search: c.Query("search"),
		Type:   c.Query("type"),
	}
	if av := c.Query("availability"); av != "" {
		b := av == "true" || av == "1" || av == "available"
		filter.Availability = &b
	}

	items, err := h.resources.List(c.Context(), filter)
	if err != nil {
		log.Printf("resources.List: %v", err)
		return apperror.Internal("failed to list resources")
	}
	if items == nil {
		items = []models.Resource{}
	}
	return c.JSON(items)
}

func (h *ResourceHandler) Get(c *fiber.Ctx) error {
	res, err := h.resources.GetByID(c.Context(), c.Params("id"))
	if err != nil {
		log.Printf("resources.GetByID(%s): %v", c.Params("id"), err)
		return apperror.Internal("failed to load resource")
	}
	if res == nil {
		return apperror.NotFound("RESOURCE_NOT_FOUND", "resource not found")
	}
	return c.JSON(res)
}

func validateResourceInput(in *models.ResourceInput) error {
	switch in.Type {
	case models.ResourceTypeRoom, models.ResourceTypeCar, models.ResourceTypeBike:
	default:
		return apperror.BadRequest("VALIDATION_ERROR", "type must be one of: room, car, bike")
	}
	if in.Name == "" {
		return apperror.BadRequest("VALIDATION_ERROR", "name is required")
	}
	return nil
}

func (h *ResourceHandler) Create(c *fiber.Ctx) error {
	var in models.ResourceInput
	if err := c.BodyParser(&in); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if err := validateResourceInput(&in); err != nil {
		return err
	}

	res, err := h.resources.Create(c.Context(), in)
	if err != nil {
		log.Printf("resources.Create: %v", err)
		return apperror.Internal("failed to create resource")
	}
	return c.Status(fiber.StatusCreated).JSON(res)
}

func (h *ResourceHandler) Update(c *fiber.Ctx) error {
	var in models.ResourceInput
	if err := c.BodyParser(&in); err != nil {
		return apperror.BadRequest("INVALID_BODY", "invalid request body")
	}
	if err := validateResourceInput(&in); err != nil {
		return err
	}

	res, err := h.resources.Update(c.Context(), c.Params("id"), in)
	if err != nil {
		log.Printf("resources.Update(%s): %v", c.Params("id"), err)
		return apperror.Internal("failed to update resource")
	}
	if res == nil {
		return apperror.NotFound("RESOURCE_NOT_FOUND", "resource not found")
	}
	return c.JSON(res)
}

func (h *ResourceHandler) Delete(c *fiber.Ctx) error {
	ok, err := h.resources.Delete(c.Context(), c.Params("id"))
	if err != nil {
		log.Printf("resources.Delete(%s): %v", c.Params("id"), err)
		return apperror.Internal("failed to delete resource")
	}
	if !ok {
		return apperror.NotFound("RESOURCE_NOT_FOUND", "resource not found")
	}
	return c.SendStatus(fiber.StatusNoContent)
}
