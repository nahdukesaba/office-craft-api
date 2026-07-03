package main

import (
	"context"
	"errors"
	"log"

	// Embeds the IANA timezone database into the binary so Asia/Jakarta
	// date-window checks (booking start/finish gating) work even on stripped
	// down Windows hosts that may not have tzdata installed.
	_ "time/tzdata"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/config"
	"office-craft-api/internal/db"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/router"
	"office-craft-api/internal/services"
)

func main() {
	cfg := config.Load()

	if err := db.RunMigrations(cfg); err != nil {
		log.Fatalf("migration error: %v", err)
	}

	pool, err := db.Connect(cfg)
	if err != nil {
		log.Fatalf("database connection error: %v", err)
	}
	defer pool.Close()

	// Seed initial admin + sample resources on first boot (idempotent).
	userRepo := repository.NewUserRepository(pool)
	resourceRepo := repository.NewResourceRepository(pool)
	seedSvc := services.NewSeedService(cfg, userRepo, resourceRepo)
	seedSvc.Run(context.Background())

	app := fiber.New(fiber.Config{
		AppName:      "Office-Craft API",
		ErrorHandler: errorHandler,
	})

	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: cfg.AllowedOrigins,
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, PUT, PATCH, DELETE, OPTIONS",
	}))

	router.Setup(app, cfg, pool)

	log.Printf("Office-Craft API listening on :%s", cfg.Port)
	if err := app.Listen(":" + cfg.Port); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// errorHandler ensures every error - both apperror.AppError (the standard
// shape: { error, message, details? }) and any plain fiber.NewError calls
// still lying around - is serialized consistently.
func errorHandler(c *fiber.Ctx, err error) error {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return c.Status(appErr.Status).JSON(appErr)
	}

	if fe, ok := err.(*fiber.Error); ok {
		return c.Status(fe.Code).JSON(apperror.AppError{
			Code:    genericCodeForStatus(fe.Code),
			Message: fe.Message,
		})
	}

	// Anything that reaches here is an unwrapped Go error (typically a raw
	// DB/repository error a handler didn't convert to an apperror). Log it
	// so a bug like this is never silent again - the response body stays
	// generic on purpose, but the real cause is now always in the server log.
	log.Printf("unhandled error on %s %s: %v", c.Method(), c.OriginalURL(), err)

	return c.Status(fiber.StatusInternalServerError).JSON(apperror.AppError{
		Code:    "INTERNAL_ERROR",
		Message: "internal server error",
	})
}

func genericCodeForStatus(status int) string {
	switch status {
	case fiber.StatusBadRequest:
		return "BAD_REQUEST"
	case fiber.StatusUnauthorized:
		return "UNAUTHORIZED"
	case fiber.StatusForbidden:
		return "FORBIDDEN"
	case fiber.StatusNotFound:
		return "NOT_FOUND"
	case fiber.StatusConflict:
		return "CONFLICT"
	default:
		return "ERROR"
	}
}
