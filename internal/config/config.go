package config

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const DefaultAllowedOrigins = "http://localhost:8081,http://127.0.0.1:8081"

// Config holds all runtime configuration for the service.
type Config struct {
	Port                           string
	DatabaseURL                    string
	MigrationsPath                 string
	SupabaseURL                    string
	SupabaseServiceKey             string
	SSOBaseURL                     string
	SSOClientID                    string
	SSORedirectURI                 string
	SSOPostLogoutRedirectURI       string
	SSOFlowHMACKey                 string
	SSOFlowTTL                     time.Duration
	SSOIntrospectionSecret         string
	AppSessionKey                  string
	AppSessionTTL                  time.Duration
	AppSessionCookieSecure         bool
	SupabaseStorageBucketResources string
	SupabaseStorageBucketProofs    string
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

	// Graph proof storage via delegated refresh token (optional - if any of
	// tenant/client/secret/refresh token is empty, proofs stay in Supabase
	// Storage). Empty SPSitePath = user's OneDrive (Plan A), set = SharePoint (Plan B).
	MSTenantID     string
	MSClientID     string
	MSClientSecret string
	MSRefreshToken string
	SPSitePath     string // e.g. kemenpkp.sharepoint.com:/sites/DokumentasiKompuSumatera2
	SPFolder       string
}

// SharePointEnabled reports whether Graph proof storage (OneDrive or SharePoint) is configured.
func (c *Config) SharePointEnabled() bool {
	return c.MSTenantID != "" && c.MSClientID != "" && c.MSClientSecret != "" &&
		c.MSRefreshToken != ""
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
		SupabaseServiceKey:             getEnv("SUPABASE_SERVICE_ROLE_KEY", ""),
		SSOBaseURL:                     getEnv("SSO_BASE_URL", "http://127.0.0.1:8082"),
		SSOClientID:                    getEnv("SSO_CLIENT_ID", "silapet-web"),
		SSORedirectURI:                 getEnv("SSO_REDIRECT_URI", "http://localhost:8081/auth/sso/callback"),
		SSOPostLogoutRedirectURI:       getEnv("SSO_POST_LOGOUT_REDIRECT_URI", "http://localhost:8081/login"),
		SSOFlowHMACKey:                 getEnv("SSO_FLOW_HMAC_KEY", ""),
		SSOFlowTTL:                     getEnvDuration("SSO_FLOW_TTL", 5*time.Minute),
		SSOIntrospectionSecret:         getEnv("SSO_INTROSPECTION_SECRET", ""),
		AppSessionKey:                  getEnv("APP_SESSION_KEY", ""),
		AppSessionTTL:                  getEnvDuration("APP_SESSION_TTL", 7*24*time.Hour),
		AppSessionCookieSecure:         getEnvBool("APP_SESSION_COOKIE_SECURE", true),
		SupabaseStorageBucketResources: getEnv("SUPABASE_STORAGE_BUCKET_RESOURCES", "resource-photos"),
		SupabaseStorageBucketProofs:    getEnv("SUPABASE_STORAGE_BUCKET_PROOFS", "booking-proofs"),
		AllowedOrigins:                 getEnv("ALLOWED_ORIGINS", DefaultAllowedOrigins),

		SMTPHost:      getEnv("SMTP_HOST", ""),
		SMTPPort:      getEnvInt("SMTP_PORT", 587),
		SMTPUsername:  getEnv("SMTP_USERNAME", ""),
		SMTPPassword:  getEnv("SMTP_PASSWORD", ""),
		SMTPFromName:  getEnv("SMTP_FROM_NAME", "Office-Craft"),
		SMTPFromEmail: getEnv("SMTP_FROM_EMAIL", ""),

		OpenWABaseURL:   getEnv("OPENWA_BASE_URL", ""),
		OpenWAAPIKey:    getEnv("OPENWA_API_KEY", ""),
		OpenWASessionID: getEnv("OPENWA_SESSION_ID", ""),

		MSTenantID:     strings.TrimSpace(getEnv("MS_TENANT_ID", "")),
		MSClientID:     strings.TrimSpace(getEnv("MS_CLIENT_ID", "")),
		MSClientSecret: strings.TrimSpace(getEnv("MS_CLIENT_SECRET", "")),
		MSRefreshToken: strings.TrimSpace(getEnv("MS_REFRESH_TOKEN", "")),
		SPSitePath:     strings.TrimSpace(getEnv("SP_SITE_PATH", "")),
		SPFolder:       strings.TrimSpace(getEnv("SP_FOLDER", "booking-proofs")),
	}

	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required (Supabase Postgres connection string)")
	}
	if cfg.SupabaseURL == "" {
		log.Fatal("SUPABASE_URL is required")
	}
	if cfg.SupabaseServiceKey == "" {
		log.Println("SUPABASE_SERVICE_ROLE_KEY not set: server-side Supabase Storage uploads and signed URLs will be unavailable")
	}
	if cfg.SMTPHost == "" {
		log.Println("SMTP_HOST not set: email notifications will be logged only, not actually sent")
	}
	if cfg.OpenWABaseURL == "" {
		log.Println("OPENWA_BASE_URL not set: WhatsApp notifications will be logged only, not actually sent")
	}
	if !cfg.SharePointEnabled() {
		log.Println("MS_* not fully set: booking proofs will be stored in Supabase Storage, not OneDrive/SharePoint")
	}

	return cfg
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
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

func getEnvBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
