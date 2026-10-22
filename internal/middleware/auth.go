package middleware

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
)

const (
	LocalsUserID      = "userID"
	LocalsEmail       = "email"
	LocalsRole        = "role"
	LocalsLoginSource = "loginSource"

	supabaseAudience = "authenticated"
	silapetCode      = "silapet"
)

type supabaseClaims struct {
	AppUserID             string                         `json:"app_user_id"`
	AppRoles              map[string]string              `json:"app_roles"`
	SessionID             string                         `json:"session_id"`
	Role                  string                         `json:"role"`
	Email                 string                         `json:"email"`
	AuthenticationMethods []supabaseAuthenticationMethod `json:"amr"`
	jwt.RegisteredClaims
}

type supabaseAuthenticationMethod struct {
	Method string `json:"method"`
}

type VerifiedAccessToken struct {
	AuthUserID  string
	AppUserID   string
	SessionID   string
	AppRole     string
	LoginSource models.LoginSource
}

type AccessTokenVerifier interface {
	Verify(
		ctx context.Context,
		token string,
	) (*VerifiedAccessToken, error)
}

type AuthenticationContextStore interface {
	GetAuthenticationContext(
		ctx context.Context,
		appUserID string,
		sessionID string,
		applicationCode string,
	) (*models.AuthenticationContext, error)
}

type jwk struct {
	KTY string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	KID string `json:"kid"`
	CRV string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwksResponse struct {
	Keys []jwk `json:"keys"`
}

// TokenVerifier verifies Supabase ES256 access tokens against the project's
// public JWKS. Known keys are cached; an unknown kid triggers one synchronized
// refresh so signing-key rotation does not require a process restart.
type TokenVerifier struct {
	issuer  string
	jwksURL string
	client  *http.Client

	keysMu    sync.RWMutex
	keys      map[string]*ecdsa.PublicKey
	refreshMu sync.Mutex
}

func NewTokenVerifier(supabaseURL string) *TokenVerifier {
	baseURL := strings.TrimRight(strings.TrimSpace(supabaseURL), "/")
	issuer := baseURL + "/auth/v1"

	return &TokenVerifier{
		issuer:  issuer,
		jwksURL: issuer + "/.well-known/jwks.json",
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		keys: make(map[string]*ecdsa.PublicKey),
	}
}

func (v *TokenVerifier) Verify(
	ctx context.Context,
	tokenString string,
) (*VerifiedAccessToken, error) {
	tokenString = strings.TrimSpace(tokenString)
	if tokenString == "" {
		return nil, errors.New("access token is required")
	}

	unverified, _, err := jwt.NewParser().ParseUnverified(
		tokenString,
		&supabaseClaims{},
	)
	if err != nil {
		return nil, fmt.Errorf("malformed access token: %w", err)
	}

	if unverified.Method.Alg() != jwt.SigningMethodES256.Alg() {
		return nil, errors.New("access token must use ES256")
	}

	kid, _ := unverified.Header["kid"].(string)
	kid = strings.TrimSpace(kid)
	if kid == "" {
		return nil, errors.New("access token kid is required")
	}

	key, err := v.keyForID(ctx, kid)
	if err != nil {
		return nil, err
	}

	claims := &supabaseClaims{}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(supabaseAudience),
		jwt.WithExpirationRequired(),
	)

	parsed, err := parser.ParseWithClaims(
		tokenString,
		claims,
		func(token *jwt.Token) (interface{}, error) {
			if token.Method != jwt.SigningMethodES256 {
				return nil, errors.New("unexpected signing method")
			}
			if tokenKID, _ := token.Header["kid"].(string); tokenKID != kid {
				return nil, errors.New("access token kid changed during verification")
			}
			return key, nil
		},
	)
	if err != nil || parsed == nil || !parsed.Valid {
		if err == nil {
			err = errors.New("access token is invalid")
		}
		return nil, fmt.Errorf("verify access token: %w", err)
	}

	if claims.Role != "" && claims.Role != supabaseAudience {
		return nil, errors.New("invalid Supabase token role")
	}

	authUserID, err := validUUIDClaim("sub", claims.Subject)
	if err != nil {
		return nil, err
	}
	appUserID, err := validUUIDClaim("app_user_id", claims.AppUserID)
	if err != nil {
		return nil, err
	}
	sessionID, err := validUUIDClaim("session_id", claims.SessionID)
	if err != nil {
		return nil, err
	}

	appRole := strings.TrimSpace(claims.AppRoles[silapetCode])
	if appRole != models.RoleUser && appRole != models.RoleAdmin {
		return nil, errors.New("valid app_roles.silapet is required")
	}

	return &VerifiedAccessToken{
		AuthUserID:  authUserID,
		AppUserID:   appUserID,
		SessionID:   sessionID,
		AppRole:     appRole,
		LoginSource: loginSourceFromAuthenticationMethods(claims.AuthenticationMethods),
	}, nil
}

func loginSourceFromAuthenticationMethods(methods []supabaseAuthenticationMethod) models.LoginSource {
	for _, method := range methods {
		switch strings.ToLower(strings.TrimSpace(method.Method)) {
		case "otp", "magiclink", "sso", "saml":
			return models.LoginSourceSSO
		}
	}
	return models.LoginSourceDirect
}

func validUUIDClaim(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil {
		return "", fmt.Errorf("%s must be a valid UUID", name)
	}
	return parsed.String(), nil
}

func (v *TokenVerifier) keyForID(
	ctx context.Context,
	kid string,
) (*ecdsa.PublicKey, error) {
	if key := v.cachedKey(kid); key != nil {
		return key, nil
	}

	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()

	if key := v.cachedKey(kid); key != nil {
		return key, nil
	}

	if err := v.refreshKeys(ctx); err != nil {
		return nil, err
	}
	if key := v.cachedKey(kid); key != nil {
		return key, nil
	}

	return nil, errors.New("access token signing key is unknown")
}

func (v *TokenVerifier) cachedKey(kid string) *ecdsa.PublicKey {
	v.keysMu.RLock()
	defer v.keysMu.RUnlock()
	return v.keys[kid]
}

func (v *TokenVerifier) refreshKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return errors.New("failed to prepare JWKS request")
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch Supabase JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("fetch Supabase JWKS: HTTP %d", resp.StatusCode)
	}

	var document jwksResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 256*1024))
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode Supabase JWKS: %w", err)
	}

	keys := make(map[string]*ecdsa.PublicKey)
	for _, candidate := range document.Keys {
		key, err := parseES256JWK(candidate)
		if err != nil {
			continue
		}
		keys[candidate.KID] = key
	}
	if len(keys) == 0 {
		return errors.New("Supabase JWKS contains no usable ES256 keys")
	}

	v.keysMu.Lock()
	v.keys = keys
	v.keysMu.Unlock()
	return nil
}

func parseES256JWK(candidate jwk) (*ecdsa.PublicKey, error) {
	if candidate.KTY != "EC" || candidate.CRV != "P-256" ||
		candidate.Alg != "ES256" || strings.TrimSpace(candidate.KID) == "" ||
		(candidate.Use != "" && candidate.Use != "sig") {
		return nil, errors.New("unsupported JWK")
	}

	xBytes, err := base64.RawURLEncoding.DecodeString(candidate.X)
	if err != nil {
		return nil, errors.New("invalid JWK x coordinate")
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(candidate.Y)
	if err != nil {
		return nil, errors.New("invalid JWK y coordinate")
	}

	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	curve := elliptic.P256()
	if x.Sign() <= 0 || y.Sign() <= 0 || !curve.IsOnCurve(x, y) {
		return nil, errors.New("invalid ES256 public key")
	}

	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

// RequireAuth validates a Supabase access token locally, then checks the
// current Supabase session, Central SSO identity mapping, and SILAPET
// membership in PostgreSQL. It never treats JWT sub as a business user ID.
func RequireAuth(
	verifier AccessTokenVerifier,
	store AuthenticationContextStore,
) fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := strings.TrimSpace(c.Get("Authorization"))
		parts := strings.Fields(authHeader)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") ||
			strings.TrimSpace(parts[1]) == "" {

			return apperror.Unauthorized(
				"missing or malformed Authorization header",
			)
		}

		if verifier == nil || store == nil {
			return apperror.New(
				fiber.StatusServiceUnavailable,
				"AUTH_UNAVAILABLE",
				"authentication service is unavailable",
			)
		}

		claims, err := verifier.Verify(c.UserContext(), parts[1])
		if err != nil {
			return apperror.Unauthorized("invalid or expired token")
		}

		identity, err := store.GetAuthenticationContext(
			c.UserContext(),
			claims.AppUserID,
			claims.SessionID,
			silapetCode,
		)
		if err != nil {
			return apperror.New(
				fiber.StatusServiceUnavailable,
				"AUTH_UNAVAILABLE",
				"authentication state is unavailable",
			)
		}

		if err := validateAuthenticationContext(claims, identity); err != nil {
			return err
		}

		c.Locals(LocalsUserID, identity.AppUserID)
		c.Locals(LocalsEmail, identity.Email)
		c.Locals(LocalsRole, identity.MembershipRole)
		c.Locals(LocalsLoginSource, claims.LoginSource)
		return c.Next()
	}
}

func validateAuthenticationContext(
	claims *VerifiedAccessToken,
	identity *models.AuthenticationContext,
) error {
	if identity == nil || !identity.SessionFound ||
		identity.SessionUserID != claims.AuthUserID {

		return apperror.Unauthorized("invalid or expired token")
	}

	if identity.AppUserID != claims.AppUserID ||
		identity.AuthUserID != claims.AuthUserID ||
		identity.UserDeleted {

		return apperror.Unauthorized("invalid token identity mapping")
	}

	if !identity.ApplicationFound || !identity.ApplicationActive ||
		identity.ApplicationCode != silapetCode ||
		!identity.MembershipFound || identity.MembershipDeleted {

		return apperror.Forbidden(
			"APPLICATION_ACCESS_UNAVAILABLE",
			"your account does not have active access to SILAPET",
		)
	}

	switch identity.MembershipStatus {
	case models.UserStatusApproved:
		// Continue.
	case models.UserStatusPending:
		return apperror.Forbidden(
			"ACCOUNT_PENDING_APPROVAL",
			"your account is awaiting admin approval",
		)
	case models.UserStatusRejected:
		return apperror.Forbidden(
			"ACCOUNT_REJECTED",
			"your account access request was rejected",
		)
	case models.UserStatusDisabled:
		return apperror.Forbidden(
			"ACCOUNT_DISABLED",
			"your SILAPET access is disabled",
		)
	default:
		return apperror.Forbidden(
			"APPLICATION_ACCESS_UNAVAILABLE",
			"your account does not have active access to SILAPET",
		)
	}

	if identity.MembershipRole != models.RoleUser &&
		identity.MembershipRole != models.RoleAdmin {

		return apperror.Forbidden(
			"APPLICATION_ACCESS_UNAVAILABLE",
			"your account has an invalid SILAPET role",
		)
	}

	if claims.AppRole != identity.MembershipRole {
		return apperror.New(
			fiber.StatusUnauthorized,
			"TOKEN_AUTHORIZATION_STALE",
			"token authorization is stale; refresh or sign in again",
		)
	}

	return nil
}

func UserIDFromCtx(c *fiber.Ctx) string {
	v, _ := c.Locals(LocalsUserID).(string)
	return v
}

func RoleFromCtx(c *fiber.Ctx) string {
	v, _ := c.Locals(LocalsRole).(string)
	return v
}

func LoginSourceFromCtx(c *fiber.Ctx) models.LoginSource {
	value, _ := c.Locals(LocalsLoginSource).(models.LoginSource)
	if value != models.LoginSourceSSO {
		return models.LoginSourceDirect
	}
	return value
}
