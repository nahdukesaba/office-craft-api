package middleware

import (
	"context"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/models"
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

// RequireAuth validates the Authorization: Bearer <token> header against the
// Supabase project's JWT secret (HS256), then loads the matching app_users
// row (for our own app-level role) into fiber locals.
func RequireAuth(jwtSecret string, pool *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			return fiber.NewError(fiber.StatusUnauthorized, "missing or malformed Authorization header")
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

		claims := &supabaseClaims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fiber.NewError(fiber.StatusUnauthorized, "unexpected signing method")
			}
			return []byte(jwtSecret), nil
		})
		if err != nil || !token.Valid {
			return fiber.NewError(fiber.StatusUnauthorized, "invalid or expired token")
		}
		if claims.Sub == "" {
			return fiber.NewError(fiber.StatusUnauthorized, "token missing subject")
		}

		var user models.AppUser
		row := pool.QueryRow(context.Background(),
			`SELECT id, email, full_name, role, created_at, updated_at
			 FROM public.app_users WHERE id = $1`, claims.Sub)
		if err := row.Scan(&user.ID, &user.Email, &user.FullName, &user.Role, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "user profile not found")
		}

		c.Locals(LocalsUserID, user.ID)
		c.Locals(LocalsEmail, user.Email)
		c.Locals(LocalsRole, user.Role)
		return c.Next()
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
