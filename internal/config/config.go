package config

import (
	"log"
	"os"

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

	return cfg
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
