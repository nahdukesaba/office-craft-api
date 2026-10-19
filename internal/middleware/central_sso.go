package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
)

type centralApplicationContext struct {
	UserID      string `json:"userId"`
	Application string `json:"application"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

type centralErrorPayload struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type resolvedCentralIdentity struct {
	UserID string
	Email  string
	Role   string
}

type cachedCentralIdentity struct {
	identity  resolvedCentralIdentity
	expiresAt time.Time
}

type inflightCentralCall struct {
	done     chan struct{}
	identity resolvedCentralIdentity
	err      error
}

const centralPositiveCacheTTL = 15 * time.Second

var (
	centralSSOHTTPClient = &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 32,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	centralCacheMu  sync.Mutex
	centralPosCache = make(map[string]cachedCentralIdentity)
	centralInflight = make(map[string]*inflightCentralCall)
)

func centralCacheKey(ssoBaseURL, accessToken string) string {
	sum := sha256.Sum256([]byte(ssoBaseURL + "\x00" + accessToken))
	return hex.EncodeToString(sum[:])
}

// InvalidateCentralSSOAuthCache evicts cached positive token validations.
// Call this on logout, password change, or membership status updates so
// revocations take effect immediately without waiting for TTL expiry.
func InvalidateCentralSSOAuthCache() {
	centralCacheMu.Lock()
	centralPosCache = make(map[string]cachedCentralIdentity)
	centralCacheMu.Unlock()
}

func requireCentralSSOAuth(
	c *fiber.Ctx,
	pool *pgxpool.Pool,
	ssoBaseURL,
	accessToken string,
) error {
	ssoBaseURL = strings.TrimRight(
		strings.TrimSpace(ssoBaseURL),
		"/",
	)
	accessToken = strings.TrimSpace(accessToken)

	if ssoBaseURL == "" || accessToken == "" {
		return apperror.Unauthorized(
			"invalid or expired token",
		)
	}

	key := centralCacheKey(ssoBaseURL, accessToken)
	now := time.Now()

	if pool != nil {
		centralCacheMu.Lock()
		if entry, ok := centralPosCache[key]; ok {
			if now.Before(entry.expiresAt) {
				centralCacheMu.Unlock()
				c.Locals(LocalsUserID, entry.identity.UserID)
				c.Locals(LocalsEmail, entry.identity.Email)
				c.Locals(LocalsRole, entry.identity.Role)
				return c.Next()
			}
			delete(centralPosCache, key)
		}
		if call, ok := centralInflight[key]; ok {
			centralCacheMu.Unlock()
			<-call.done
			if call.err != nil {
				return call.err
			}
			c.Locals(LocalsUserID, call.identity.UserID)
			c.Locals(LocalsEmail, call.identity.Email)
			c.Locals(LocalsRole, call.identity.Role)
			return c.Next()
		}
		call := &inflightCentralCall{done: make(chan struct{})}
		centralInflight[key] = call
		centralCacheMu.Unlock()

		identity, err := resolveCentralSSOIdentity(c, pool, ssoBaseURL, accessToken)

		centralCacheMu.Lock()
		call.identity = identity
		call.err = err
		delete(centralInflight, key)
		if err == nil {
			centralPosCache[key] = cachedCentralIdentity{
				identity:  identity,
				expiresAt: time.Now().Add(centralPositiveCacheTTL),
			}
		}
		close(call.done)
		centralCacheMu.Unlock()

		if err != nil {
			return err
		}
		c.Locals(LocalsUserID, identity.UserID)
		c.Locals(LocalsEmail, identity.Email)
		c.Locals(LocalsRole, identity.Role)
		return c.Next()
	}

	identity, err := resolveCentralSSOIdentity(c, pool, ssoBaseURL, accessToken)
	if err != nil {
		return err
	}
	c.Locals(LocalsUserID, identity.UserID)
	c.Locals(LocalsEmail, identity.Email)
	c.Locals(LocalsRole, identity.Role)
	return c.Next()
}

func resolveCentralSSOIdentity(
	c *fiber.Ctx,
	pool *pgxpool.Pool,
	ssoBaseURL,
	accessToken string,
) (resolvedCentralIdentity, error) {
	endpoint := ssoBaseURL +
		"/api/v1/auth/application-context?application=" +
		url.QueryEscape(models.ApplicationCodeSilapet)

	reqCtx := context.Background()
	if c != nil && c.UserContext() != nil {
		reqCtx = c.UserContext()
	}

	req, err := http.NewRequestWithContext(
		reqCtx,
		http.MethodGet,
		endpoint,
		nil,
	)
	if err != nil {
		return resolvedCentralIdentity{}, apperror.Internal(
			"failed to prepare central authentication request",
		)
	}

	req.Header.Set(
		"Authorization",
		"Bearer "+accessToken,
	)
	req.Header.Set("Accept", "application/json")

	resp, err := centralSSOHTTPClient.Do(req)
	if err != nil {
		log.Printf("central SSO application-context request failed: %v", err)
		return resolvedCentralIdentity{}, apperror.New(
			fiber.StatusServiceUnavailable,
			"AUTH_UNAVAILABLE",
			"central authentication service is unavailable",
		)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		// Continue.

	case http.StatusUnauthorized:
		return resolvedCentralIdentity{}, apperror.Unauthorized(
			"invalid or expired token",
		)

	case http.StatusForbidden:
		var errPayload centralErrorPayload
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&errPayload)
		switch strings.ToLower(strings.TrimSpace(errPayload.Error)) {
		case "account_pending":
			return resolvedCentralIdentity{}, apperror.Forbidden(
				"ACCOUNT_PENDING_APPROVAL",
				"your account is awaiting admin approval",
			)
		case "account_rejected":
			return resolvedCentralIdentity{}, apperror.Forbidden(
				"ACCOUNT_REJECTED",
				"your account access request was rejected",
			)
		default:
			return resolvedCentralIdentity{}, apperror.Forbidden(
				"APPLICATION_ACCESS_UNAVAILABLE",
				"your account does not have access to SILAPET",
			)
		}

	case http.StatusNotFound:
		return resolvedCentralIdentity{}, apperror.Forbidden(
			"APPLICATION_ACCESS_UNAVAILABLE",
			"SILAPET application access is unavailable",
		)

	default:
		log.Printf("central SSO application-context returned HTTP %d", resp.StatusCode)
		return resolvedCentralIdentity{}, apperror.New(
			fiber.StatusServiceUnavailable,
			"AUTH_UNAVAILABLE",
			"central authentication service is unavailable",
		)
	}

	var appContext centralApplicationContext

	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&appContext); err != nil {
		return resolvedCentralIdentity{}, apperror.Internal(
			"failed to decode central authentication response",
		)
	}

	appContext.UserID = strings.TrimSpace(appContext.UserID)
	appContext.Application = strings.TrimSpace(appContext.Application)
	appContext.Role = strings.TrimSpace(appContext.Role)
	appContext.Status = strings.TrimSpace(appContext.Status)

	if appContext.UserID == "" ||
		!strings.EqualFold(
			appContext.Application,
			models.ApplicationCodeSilapet,
		) ||
		appContext.Role == "" ||
		appContext.Status == "" {

		return resolvedCentralIdentity{}, apperror.Unauthorized(
			"invalid central authentication context",
		)
	}

	switch appContext.Status {
	case models.UserStatusApproved:
		// Allowed.

	case models.UserStatusPending:
		return resolvedCentralIdentity{}, apperror.Forbidden(
			"ACCOUNT_PENDING_APPROVAL",
			"your account is awaiting admin approval",
		)

	case models.UserStatusRejected:
		return resolvedCentralIdentity{}, apperror.Forbidden(
			"ACCOUNT_REJECTED",
			"your account access request was rejected",
		)

	case models.UserStatusDisabled:
		return resolvedCentralIdentity{}, apperror.Forbidden(
			"ACCOUNT_DISABLED",
			"your SILAPET access is disabled",
		)

	default:
		return resolvedCentralIdentity{}, apperror.Forbidden(
			"APPLICATION_ACCESS_UNAVAILABLE",
			"your account does not have active access to SILAPET",
		)
	}

	// Application authorization is already resolved by Central SSO.
	// Fetch only the local profile email required by SILAPET request locals.
	var email string

	err = pool.QueryRow(
		c.Context(),
		`
			SELECT email
			FROM public.app_users
			WHERE id = $1
			  AND deleted_at IS NULL
		`,
		appContext.UserID,
	).Scan(&email)

	if err != nil {
		if err == pgx.ErrNoRows {
			return resolvedCentralIdentity{}, apperror.Unauthorized(
				"central identity is unavailable locally",
			)
		}

		return resolvedCentralIdentity{}, apperror.Internal(
			"failed to resolve central SILAPET identity",
		)
	}

	return resolvedCentralIdentity{
		UserID: appContext.UserID,
		Email:  strings.TrimSpace(email),
		Role:   appContext.Role,
	}, nil
}
