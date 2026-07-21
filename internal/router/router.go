package router

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/config"
	"office-craft-api/internal/handlers"
	"office-craft-api/internal/middleware"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/services"
)

// Setup wires repositories -> services -> handlers -> routes onto the fiber app.
func Setup(app *fiber.App, cfg *config.Config, pool *pgxpool.Pool) {
	userRepo := repository.NewUserRepository(pool)
	resourceRepo := repository.NewResourceRepository(pool)
	bookingRepo := repository.NewBookingRepository(pool)
	proofRepo := repository.NewProofRepository(pool)
	eventRepo := repository.NewBookingEventRepository(pool)

	authSvc := services.NewAuthService(cfg, userRepo)
	emailSvc := services.NewEmailService(cfg)
	whatsappSvc := services.NewWhatsAppService(cfg)
	notifySvc := services.NewNotifyService(bookingRepo, userRepo, resourceRepo, emailSvc, whatsappSvc)
	bookingSvc := services.NewBookingService(bookingRepo, resourceRepo, userRepo, proofRepo, eventRepo, notifySvc)
	proofSvc := services.NewProofService(bookingRepo)
	reportSvc := services.NewReportService(bookingRepo, resourceRepo, userRepo)

	authHandler := handlers.NewAuthHandler(authSvc, userRepo, emailSvc)
	resourceHandler := handlers.NewResourceHandler(resourceRepo)
	bookingHandler := handlers.NewBookingHandler(bookingRepo, resourceRepo, userRepo, proofRepo, eventRepo, bookingSvc)
	proofHandler := handlers.NewProofHandler(proofRepo, bookingRepo, proofSvc)
	notifyHandler := handlers.NewNotifyHandler(notifySvc, bookingRepo)
	publicHandler := handlers.NewPublicHandler(bookingRepo, resourceRepo, userRepo)
	statsHandler := handlers.NewStatsHandler(bookingRepo, resourceRepo, userRepo)
	userHandler := handlers.NewUserHandler(userRepo)
	reportHandler := handlers.NewReportHandler(reportSvc)

	verifier := middleware.NewTokenVerifier(cfg.SupabaseJWTSecret, cfg.SupabaseURL)
	requireAuth := middleware.RequireAuth(verifier, pool)
	requireAdmin := middleware.RequireAdmin()

	api := app.Group("/api")

	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// -------- Auth --------
	auth := api.Group("/auth")
	auth.Post("/login", authHandler.Login)
	auth.Post("/register", authHandler.Register)
	auth.Get("/me", requireAuth, authHandler.Me)
	auth.Put("/password", requireAuth, authHandler.ChangePassword)
	auth.Put("/reset", requireAuth, requireAdmin, authHandler.AdminResetPassword)

	// -------- Public (no auth) --------
	public := api.Group("/public")
	public.Get("/bookings/all", publicHandler.AllBookings)
	public.Get("/bookings/resource/:resourceId", publicHandler.BookingsForResource)

	// -------- Resources --------
	resources := api.Group("/resources")
	resources.Get("/", resourceHandler.List)
	resources.Get("/:id", resourceHandler.Get, requireAuth)
	resources.Post("/", resourceHandler.Create, requireAuth, requireAdmin)
	resources.Put("/:id", resourceHandler.Update, requireAuth, requireAdmin)
	resources.Delete("/:id", resourceHandler.Delete, requireAuth, requireAdmin)

	// -------- Bookings --------
	// Lifecycle: pending -> approved -> in_use -> finished -> closed, with
	// rejected/cancelled as terminal off-ramps and needs_revision as a revision state.
	bookings := api.Group("/bookings", requireAuth)
	bookings.Get("/", bookingHandler.List)
	bookings.Get("/:id", bookingHandler.Get)
	bookings.Post("/", bookingHandler.Create)
	bookings.Put("/:id/approve", requireAdmin, bookingHandler.Approve)
	bookings.Put("/:id/reject", requireAdmin, bookingHandler.Reject)
	bookings.Put("/:id/revoke", requireAdmin, bookingHandler.Revoke)
	bookings.Put("/:id/start", bookingHandler.Start) // owner or admin, gated by date window in the service
	bookings.Put("/:id/finish", bookingHandler.Finish)
	bookings.Put("/:id/cancel", bookingHandler.Cancel)
	bookings.Put("/:id/close", requireAdmin, bookingHandler.Close)
	bookings.Put("/:id/request-revision", requireAdmin, bookingHandler.RequestRevision)
	bookings.Post("/:id/notify", notifyHandler.Notify)
	bookings.Get("/:id/history", bookingHandler.History)

	// -------- Proofs (nested under bookings, still requires auth) --------
	bookings.Get("/:bookingId/proofs", proofHandler.List)
	bookings.Post("/:bookingId/proofs", proofHandler.Create)

	// -------- Stats --------
	stats := api.Group("/stats", requireAuth, requireAdmin)
	stats.Get("/overview", statsHandler.Overview)

	// -------- Users (admin: approve/reject registrations) --------
	users := api.Group("/users", requireAuth, requireAdmin)
	users.Get("/", userHandler.List)
	users.Post("/:id/approve", userHandler.Approve)
	users.Post("/:id/reject", userHandler.Reject)

	// -------- Reports (admin: date-range export & insights) --------
	reports := api.Group("/reports", requireAuth, requireAdmin)
	reports.Get("/bookings/export", reportHandler.Export)
	reports.Get("/bookings/insights", reportHandler.Insights)
}
