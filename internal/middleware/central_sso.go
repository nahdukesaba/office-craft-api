package middleware

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
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

var centralSSOHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
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

	endpoint := ssoBaseURL +
		"/api/v1/auth/application-context?application=" +
		url.QueryEscape(models.ApplicationCodeSilapet)

	req, err := http.NewRequestWithContext(
		c.UserContext(),
		http.MethodGet,
		endpoint,
		nil,
	)
	if err != nil {
		return apperror.Internal(
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
		return fiber.NewError(
			fiber.StatusServiceUnavailable,
			"central authentication service is unavailable",
		)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Continue.

	case http.StatusUnauthorized:
		return apperror.Unauthorized(
			"invalid or expired token",
		)

	case http.StatusForbidden:
		return apperror.Forbidden(
			"APPLICATION_ACCESS_UNAVAILABLE",
			"your account does not have access to SILAPET",
		)

	case http.StatusNotFound:
		return apperror.Forbidden(
			"APPLICATION_ACCESS_UNAVAILABLE",
			"SILAPET application access is unavailable",
		)

	default:
		return fiber.NewError(
			fiber.StatusServiceUnavailable,
			"central authentication service is unavailable",
		)
	}

	var appContext centralApplicationContext

	if err := json.NewDecoder(resp.Body).Decode(&appContext); err != nil {
		return apperror.Internal(
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

		return apperror.Unauthorized(
			"invalid central authentication context",
		)
	}

	switch appContext.Status {
	case models.UserStatusApproved:
		// Allowed.

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
			return apperror.Unauthorized(
				"central identity is unavailable locally",
			)
		}

		return apperror.Internal(
			"failed to resolve central SILAPET identity",
		)
	}

	c.Locals(LocalsUserID, appContext.UserID)
	c.Locals(LocalsEmail, strings.TrimSpace(email))
	c.Locals(LocalsRole, appContext.Role)

	return c.Next()
}
