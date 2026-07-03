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

	authSvc := services.NewAuthService(cfg, userRepo)
	bookingSvc := services.NewBookingService(bookingRepo, resourceRepo, userRepo, proofRepo)
	proofSvc := services.NewProofService(bookingRepo)
	// Swap services.LogNotifier{} for a real email/SMS/webhook Notifier once
	// you have a provider - NotifyService and the handler don't need to change.
	notifySvc := services.NewNotifyService(bookingRepo, userRepo, services.LogNotifier{})

	authHandler := handlers.NewAuthHandler(authSvc, userRepo)
	resourceHandler := handlers.NewResourceHandler(resourceRepo)
	bookingHandler := handlers.NewBookingHandler(bookingRepo, resourceRepo, userRepo, bookingSvc)
	proofHandler := handlers.NewProofHandler(proofRepo, bookingRepo, proofSvc)
	notifyHandler := handlers.NewNotifyHandler(notifySvc, bookingRepo)
	publicHandler := handlers.NewPublicHandler(bookingRepo, resourceRepo)
	statsHandler := handlers.NewStatsHandler(bookingRepo, resourceRepo, userRepo)
	userHandler := handlers.NewUserHandler(userRepo)

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

	// -------- Public (no auth) --------
	public := api.Group("/public")
	public.Get("/bookings/all", publicHandler.AllBookings)
	public.Get("/bookings/resource/:resourceId", publicHandler.BookingsForResource)

	// -------- Resources --------
	resources := api.Group("/resources", requireAuth)
	resources.Get("/", resourceHandler.List)
	resources.Get("/:id", resourceHandler.Get)
	resources.Post("/", requireAdmin, resourceHandler.Create)
	resources.Put("/:id", requireAdmin, resourceHandler.Update)
	resources.Delete("/:id", requireAdmin, resourceHandler.Delete)

	// -------- Bookings --------
	// Lifecycle: pending -> approved -> in_use -> finished, with
	// rejected/cancelled as terminal off-ramps.
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
	bookings.Post("/:id/notify", notifyHandler.Notify)

	// -------- Proofs (nested under bookings, still requires auth) --------
	bookings.Get("/:bookingId/proofs", proofHandler.List)
	bookings.Post("/:bookingId/proofs", proofHandler.Create)

	// -------- Stats --------
	stats := api.Group("/stats", requireAuth, requireAdmin)
	stats.Get("/overview", statsHandler.Overview)

	// -------- Users (admin: approve/reject registrations) --------
	users := api.Group("/users", requireAuth, requireAdmin)
	users.Get("/", userHandler.List)
	users.Put("/:id/approve", userHandler.Approve)
	users.Put("/:id/reject", userHandler.Reject)
}
