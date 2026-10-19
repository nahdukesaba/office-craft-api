package middleware

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/apperror"
)

const (
	LocalsUserID = "userID"
	LocalsEmail  = "email"
	LocalsRole   = "role"
)

// supabaseClaims models the subset of a Supabase access token's JWT claims
// that we care about.
type supabaseClaims struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Role  string `json:"role"` // supabase's own "authenticated" role, not app role
	jwt.RegisteredClaims
}

// TokenVerifier validates Supabase-issued access tokens under either signing
// scheme Supabase uses:
//   - Legacy projects: HS256, signed with a shared secret (SUPABASE_JWT_SECRET).
//   - Projects created after ~May 2025: ES256 (or RS256), signed with an
//     asymmetric key you verify against Supabase's public JWKS endpoint.
//
// The token's own "alg" header tells us which path to take, so both keep
// working without any config flag - this matters because Supabase can also
// have an active *and* a standby key mid-rotation.
type TokenVerifier struct {
	jwtSecret string
	jwksURL   string

	mu   sync.Mutex
	jwks keyfunc.Keyfunc
}

func NewTokenVerifier(jwtSecret, supabaseURL string) *TokenVerifier {
	return &TokenVerifier{
		jwtSecret: jwtSecret,
		jwksURL:   strings.TrimRight(supabaseURL, "/") + "/auth/v1/.well-known/jwks.json",
	}
}

func (v *TokenVerifier) ensureJWKS(ctx context.Context) (keyfunc.Keyfunc, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.jwks != nil {
		return v.jwks, nil
	}
	k, err := keyfunc.NewDefaultCtx(ctx, []string{v.jwksURL})
	if err != nil {
		return nil, fmt.Errorf("fetching supabase JWKS from %s: %w", v.jwksURL, err)
	}
	v.jwks = k
	return k, nil
}

// Parse validates tokenStr and returns its claims.
func (v *TokenVerifier) Parse(ctx context.Context, tokenStr string) (*supabaseClaims, error) {
	unverified, _, err := jwt.NewParser().ParseUnverified(tokenStr, &supabaseClaims{})
	if err != nil {
		return nil, fmt.Errorf("malformed token: %w", err)
	}
	alg, _ := unverified.Header["alg"].(string)

	claims := &supabaseClaims{}

	if alg == "HS256" {
		if v.jwtSecret == "" {
			return nil, fmt.Errorf("token is HS256 but SUPABASE_JWT_SECRET is not configured")
		}
		_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
			}
			return []byte(v.jwtSecret), nil
		})
		if err != nil {
			return nil, err
		}
		return claims, nil
	}

	// ES256 / RS256: Supabase's current default for projects created after
	// ~May 2025. Verify against the project's published JWKS.
	jwks, err := v.ensureJWKS(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := jwt.ParseWithClaims(tokenStr, claims, jwks.Keyfunc); err != nil {
		return nil, err
	}
	return claims, nil
}

// RequireAuth delegates protected API authentication to Central SSO.
// Central SSO validates the token, session, and SILAPET membership.
func RequireAuth(
	pool *pgxpool.Pool,
	ssoBaseURL string,
) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")

		if authHeader == "" ||
			!strings.HasPrefix(authHeader, "Bearer ") {
			return apperror.Unauthorized(
				"missing or malformed Authorization header",
			)
		}

		tokenStr := strings.TrimSpace(
			strings.TrimPrefix(authHeader, "Bearer "),
		)

		if tokenStr == "" {
			return apperror.Unauthorized(
				"missing or malformed Authorization header",
			)
		}

		return requireCentralSSOAuth(
			c,
			pool,
			ssoBaseURL,
			tokenStr,
		)
	}
}

// UserIDFromCtx is a small helper for handlers.
func UserIDFromCtx(c *fiber.Ctx) string {
	v, _ := c.Locals(LocalsUserID).(string)
	return v
}

// RoleFromCtx is a small helper for handlers.
func RoleFromCtx(c *fiber.Ctx) string {
	v, _ := c.Locals(LocalsRole).(string)
	return v
}
