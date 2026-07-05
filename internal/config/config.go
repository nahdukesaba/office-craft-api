package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration for the service.
type Config struct {
	Port                           string
	DatabaseURL                    string
	MigrationsPath                 string
	SupabaseURL                    string
	SupabaseAnonKey                string
	SupabaseServiceKey             string
	SupabaseJWTSecret              string
	SupabaseStorageBucketResources string
	SupabaseStorageBucketProofs    string
	SeedAdminEmail                 string
	SeedAdminPassword              string
	SeedAdminFullName              string
	AllowedOrigins                 string

	// Email notifications (optional - if SMTPHost is empty, EmailNotifier
	// just logs instead of sending, so the app still runs without it).
	SMTPHost      string
	SMTPPort      int
	SMTPUsername  string
	SMTPPassword  string
	SMTPFromName  string
	SMTPFromEmail string

	// WhatsApp notifications via a self-hosted OpenWA gateway (optional -
	// if OpenWABaseURL is empty, WhatsAppService just logs instead of
	// sending, same graceful-degrade pattern as email).
	OpenWABaseURL   string
	OpenWAAPIKey    string
	OpenWASessionID string
}

// Load reads configuration from a .env file (if present) and the environment.
// Environment variables always take precedence over .env file values.
func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, relying on real environment variables")
	}

	cfg := &Config{
		Port:                           getEnv("PORT", "8080"),
		DatabaseURL:                    getEnv("DATABASE_URL", ""),
		MigrationsPath:                 getEnv("MIGRATIONS_PATH", "migrations"),
		SupabaseURL:                    getEnv("SUPABASE_URL", ""),
		SupabaseAnonKey:                getEnv("SUPABASE_ANON_KEY", ""),
		SupabaseServiceKey:             getEnv("SUPABASE_SERVICE_ROLE_KEY", ""),
		SupabaseJWTSecret:              getEnv("SUPABASE_JWT_SECRET", ""),
		SupabaseStorageBucketResources: getEnv("SUPABASE_STORAGE_BUCKET_RESOURCES", "resource-photos"),
		SupabaseStorageBucketProofs:    getEnv("SUPABASE_STORAGE_BUCKET_PROOFS", "booking-proofs"),
		SeedAdminEmail:                 getEnv("SEED_ADMIN_EMAIL", "admin@office-craft.local"),
		SeedAdminPassword:              getEnv("SEED_ADMIN_PASSWORD", "ChangeMe123!"),
		SeedAdminFullName:              getEnv("SEED_ADMIN_FULL_NAME", "System Administrator"),
		AllowedOrigins:                 getEnv("ALLOWED_ORIGINS", "*"),

		SMTPHost:      getEnv("SMTP_HOST", ""),
		SMTPPort:      getEnvInt("SMTP_PORT", 587),
		SMTPUsername:  getEnv("SMTP_USERNAME", ""),
		SMTPPassword:  getEnv("SMTP_PASSWORD", ""),
		SMTPFromName:  getEnv("SMTP_FROM_NAME", "Office-Craft"),
		SMTPFromEmail: getEnv("SMTP_FROM_EMAIL", ""),

		OpenWABaseURL:   getEnv("OPENWA_BASE_URL", ""),
		OpenWAAPIKey:    getEnv("OPENWA_API_KEY", ""),
		OpenWASessionID: getEnv("OPENWA_SESSION_ID", ""),
	}

	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required (Supabase Postgres connection string)")
	}
	if cfg.SupabaseURL == "" || cfg.SupabaseAnonKey == "" {
		log.Fatal("SUPABASE_URL and SUPABASE_ANON_KEY are required")
	}
	if cfg.SupabaseJWTSecret == "" {
		log.Fatal("SUPABASE_JWT_SECRET is required to validate access tokens")
	}
	if cfg.SMTPHost == "" {
		log.Println("SMTP_HOST not set: email notifications will be logged only, not actually sent")
	}
	if cfg.OpenWABaseURL == "" {
		log.Println("OPENWA_BASE_URL not set: WhatsApp notifications will be logged only, not actually sent")
	}

	return cfg
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Printf("config: invalid integer for %s=%q, using default %d", key, v, fallback)
		return fallback
	}
	return n
}
