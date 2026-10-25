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
func Setup(app *fiber.App, cfg *config.Config, pool *pgxpool.Pool, appSessions *services.AppSessionManager) {
	userRepo := repository.NewUserRepository(pool)
	ssoFlowRepo := repository.NewBrowserSSOFlowRepository(pool)
	resourceRepo := repository.NewResourceRepository(pool)
	bookingRepo := repository.NewBookingRepository(pool)
	proofRepo := repository.NewProofRepository(pool)
	eventRepo := repository.NewBookingEventRepository(pool)

	emailSvc := services.NewEmailService(cfg)
	whatsappSvc := services.NewWhatsAppService(cfg)
	notifySvc := services.NewNotifyService(bookingRepo, userRepo, resourceRepo, emailSvc, whatsappSvc)
	authSvc := services.NewAuthService(cfg, userRepo, notifySvc)
	authSvc.SetBrowserSSOFlowStore(ssoFlowRepo)
	bookingSvc := services.NewBookingService(bookingRepo, resourceRepo, userRepo, proofRepo, eventRepo, notifySvc)
	proofSvc := services.NewProofService(bookingRepo)
	reportSvc := services.NewReportService(bookingRepo, resourceRepo, userRepo)

	authHandler := handlers.NewAuthHandler(authSvc, userRepo, appSessions)
	resourceHandler := handlers.NewResourceHandler(resourceRepo)
	bookingHandler := handlers.NewBookingHandler(bookingRepo, resourceRepo, userRepo, proofRepo, eventRepo, bookingSvc)
	proofHandler := handlers.NewProofHandler(proofRepo, bookingRepo, proofSvc)
	storageHandler := handlers.NewStorageHandler(cfg, bookingRepo, proofRepo, proofSvc)
	notifyHandler := handlers.NewNotifyHandler(notifySvc, bookingRepo)
	publicHandler := handlers.NewPublicHandler(bookingRepo, resourceRepo, userRepo)
	statsHandler := handlers.NewStatsHandler(bookingRepo, resourceRepo, userRepo)
	userHandler := handlers.NewUserHandler(userRepo, notifySvc)
	reportHandler := handlers.NewReportHandler(reportSvc)

	tokenVerifier := middleware.NewTokenVerifier(cfg.SupabaseURL)
	requireAuth := middleware.RequireAuth(tokenVerifier, userRepo)
	requireSession := middleware.RequireAppSession(appSessions)
	requireAdmin := middleware.RequireAdmin()

	api := app.Group("/api")

	api.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// -------- Auth --------
	auth := api.Group("/auth")
	auth.Post("/login", authHandler.Login)
	auth.Get("/sso/start", authHandler.SSOStart)
	auth.Get("/sso/logout", authHandler.SSOLogoutStart)
	auth.Post("/sso/exchange", authHandler.SSOExchange)
	auth.Post("/register", authHandler.Register)
	auth.Post("/request-access", authHandler.RequestAccess)
	auth.Post("/forgot-password", authHandler.ForgotPassword)
	auth.Post("/reset-password", authHandler.ResetPassword)
	auth.Post("/refresh", authHandler.Refresh)
	auth.Post("/logout", requireSession, authHandler.Logout)
	auth.Get("/me", requireSession, requireAuth, authHandler.Me)
	auth.Put("/password", requireSession, requireAuth, authHandler.ChangePassword)
	auth.Put("/phone", requireSession, requireAuth, authHandler.ChangePhoneNumber)

	// -------- Public (no auth) --------
	public := api.Group("/public")
	public.Get("/bookings/all", publicHandler.AllBookings)
	public.Get("/bookings/resource/:resourceId", publicHandler.BookingsForResource)

	// -------- Resources --------
	resources := api.Group("/resources")
	resources.Get("/", resourceHandler.List)
	resources.Get("/:id", requireSession, requireAuth, resourceHandler.Get)
	resources.Post("/photos/upload", requireSession, requireAuth, requireAdmin, storageHandler.UploadResourcePhoto)
	resources.Post("/", requireSession, requireAuth, requireAdmin, resourceHandler.Create)
	resources.Put("/:id", requireSession, requireAuth, requireAdmin, resourceHandler.Update)
	resources.Delete("/:id", requireSession, requireAuth, requireAdmin, resourceHandler.Delete)

	// -------- Bookings --------
	// Lifecycle: pending -> approved -> in_use -> finished -> closed, with
	// rejected/cancelled as terminal off-ramps and needs_revision as a revision state.
	bookings := api.Group("/bookings", requireSession, requireAuth)
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
	bookings.Post("/:bookingId/proofs/upload", storageHandler.UploadProof)
	bookings.Get("/:bookingId/proofs/signed", storageHandler.ListProofsSigned)

	// -------- Stats --------
	stats := api.Group("/stats", requireSession, requireAuth, requireAdmin)
	stats.Get("/overview", statsHandler.Overview)

	// -------- Users (admin: approve/reject registrations) --------
	users := api.Group("/users", requireSession, requireAuth, requireAdmin)
	users.Get("/", userHandler.List)
	users.Post("/:id/approve", userHandler.Approve)
	users.Post("/:id/reject", userHandler.Reject)

	// -------- Reports (admin: date-range export & insights) --------
	reports := api.Group("/reports", requireSession, requireAuth, requireAdmin)
	reports.Get("/bookings/export", reportHandler.Export)
	reports.Get("/bookings/insights", reportHandler.Insights)
}
