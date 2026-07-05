// Command test-email sends one test email using the same SMTP config the
// main server reads from .env, so you can verify credentials work before
// relying on them inside the actual notify feature.
//
// Usage:
//
//	go run ./cmd/test-email you@example.com
package main

import (
	"fmt"
	"log"
	"os"

	"office-craft-api/internal/config"
	"office-craft-api/internal/services"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: go run ./cmd/test-email <recipient-email>")
	}
	to := os.Args[1]

	cfg := config.Load()
	email := services.NewEmailService(cfg)

	err := email.Send(to, "", "Office-Craft: test email",
		"<h2>It works!</h2><p>This is a test email from the Office-Craft backend's SMTP configuration.</p>")
	if err != nil {
		log.Fatalf("failed to send: %v", err)
	}
	fmt.Println("Sent successfully - check", to, "(and its spam folder, the first time).")
}
