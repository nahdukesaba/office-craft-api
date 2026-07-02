package main

import (
	"context"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

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

// errorHandler ensures every error - including fiber.NewError calls scattered
// throughout handlers - is serialized as consistent camelCase JSON.
func errorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "internal server error"

	if fe, ok := err.(*fiber.Error); ok {
		code = fe.Code
		message = fe.Message
	}

	return c.Status(code).JSON(fiber.Map{
		"error":      true,
		"message":    message,
		"statusCode": code,
	})
}
