package handlers

import (
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/services"
	"office-craft-api/internal/utils"
)

type ReportHandler struct {
	svc *services.ReportService
}

func NewReportHandler(svc *services.ReportService) *ReportHandler {
	return &ReportHandler{svc: svc}
}

// parseDateRange reads required `from`/`to` query params (YYYY-MM-DD,
// Asia/Jakarta) and returns [from-midnight, to-midnight-plus-one-day) so
// callers can use a simple half-open range comparison.
func parseDateRange(c *fiber.Ctx) (time.Time, time.Time, error) {
	loc := utils.JakartaLocation()

	fromStr := c.Query("from")
	toStr := c.Query("to")
	if fromStr == "" || toStr == "" {
		return time.Time{}, time.Time{}, apperror.BadRequest("VALIDATION_ERROR", "from and to query params are required (YYYY-MM-DD)")
	}

	from, err := time.ParseInLocation("2006-01-02", fromStr, loc)
	if err != nil {
		return time.Time{}, time.Time{}, apperror.BadRequest("VALIDATION_ERROR", "from must be a valid date (YYYY-MM-DD)")
	}
	toDay, err := time.ParseInLocation("2006-01-02", toStr, loc)
	if err != nil {
		return time.Time{}, time.Time{}, apperror.BadRequest("VALIDATION_ERROR", "to must be a valid date (YYYY-MM-DD)")
	}
	to := toDay.AddDate(0, 0, 1) // make `to` inclusive of its whole day

	if !to.After(from) {
		return time.Time{}, time.Time{}, apperror.BadRequest("VALIDATION_ERROR", "to must be on or after from")
	}

	return from, to, nil
}

func (h *ReportHandler) Export(c *fiber.Ctx) error {
	from, to, err := parseDateRange(c)
	if err != nil {
		return err
	}
	status := c.Query("status")

	csvBytes, err := h.svc.ExportBookingsCSV(c.Context(), from, to, status)
	if err != nil {
		return apperror.Internal("failed to build export")
	}

	filename := fmt.Sprintf("bookings_%s_to_%s.csv", c.Query("from"), c.Query("to"))
	c.Set("Content-Type", "text/csv")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	return c.Send(csvBytes)
}

func (h *ReportHandler) Insights(c *fiber.Ctx) error {
	from, to, err := parseDateRange(c)
	if err != nil {
		return err
	}

	insights, err := h.svc.BuildInsights(c.Context(), from, to)
	if err != nil {
		return apperror.Internal("failed to build insights")
	}
	return c.JSON(insights)
}
