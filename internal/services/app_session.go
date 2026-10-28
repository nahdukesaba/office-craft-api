package services

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"office-craft-api/internal/models"
)

const AppSessionCookieName = "silapet-session"

var ErrInvalidAppSession = errors.New("invalid application session")

type AppSessionData struct {
	AppUserID    string             `json:"appUserId"`
	Role         string             `json:"role"`
	Status       string             `json:"status"`
	LoginSource  models.LoginSource `json:"loginSource"`
	SID          string             `json:"sid,omitempty"`
	AccessToken  string             `json:"accessToken"`
	RefreshToken string             `json:"refreshToken"`
	ExpiresAt    int64              `json:"expiresAt"`
}

type AppSessionManager struct {
	aead   cipher.AEAD
	secure bool
	ttl    time.Duration
	now    func() time.Time
}

func NewAppSessionManager(encodedKey string, secure bool, ttl time.Duration) (*AppSessionManager, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(encodedKey))
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("APP_SESSION_KEY must be a base64-encoded 32-byte key")
	}
	if ttl < time.Minute {
		return nil, errors.New("APP_SESSION_TTL must be at least one minute")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize app session cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize app session cipher: %w", err)
	}
	return &AppSessionManager{aead: aead, secure: secure, ttl: ttl, now: time.Now}, nil
}

func validAppSession(data AppSessionData) bool {
	return strings.TrimSpace(data.AppUserID) != "" &&
		strings.TrimSpace(data.AccessToken) != "" &&
		strings.TrimSpace(data.RefreshToken) != "" &&
		(data.Role == models.RoleUser || data.Role == models.RoleAdmin) &&
		data.Status == models.UserStatusApproved &&
		(data.LoginSource == models.LoginSourceDirect || data.LoginSource == models.LoginSourceSSO) &&
		((data.LoginSource == models.LoginSourceDirect && strings.TrimSpace(data.SID) == "") ||
			(data.LoginSource == models.LoginSourceSSO && strings.TrimSpace(data.SID) != ""))
}

func (m *AppSessionManager) Encode(data AppSessionData) (string, error) {
	if m == nil || m.aead == nil || !validAppSession(data) {
		return "", ErrInvalidAppSession
	}
	data.ExpiresAt = m.now().Add(m.ttl).Unix()
	payload, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := m.aead.Seal(nonce, nonce, payload, []byte(AppSessionCookieName))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (m *AppSessionManager) Decode(value string) (*AppSessionData, error) {
	if m == nil || m.aead == nil {
		return nil, ErrInvalidAppSession
	}
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(sealed) <= m.aead.NonceSize() {
		return nil, ErrInvalidAppSession
	}
	nonce, ciphertext := sealed[:m.aead.NonceSize()], sealed[m.aead.NonceSize():]
	payload, err := m.aead.Open(nil, nonce, ciphertext, []byte(AppSessionCookieName))
	if err != nil {
		return nil, ErrInvalidAppSession
	}
	var data AppSessionData
	if json.Unmarshal(payload, &data) != nil || data.ExpiresAt <= m.now().Unix() || !validAppSession(data) {
		return nil, ErrInvalidAppSession
	}
	return &data, nil
}

func (m *AppSessionManager) Read(c *fiber.Ctx) (*AppSessionData, error) {
	return m.Decode(c.Cookies(AppSessionCookieName))
}

func (m *AppSessionManager) Set(c *fiber.Ctx, data AppSessionData) error {
	value, err := m.Encode(data)
	if err != nil {
		return err
	}
	c.Cookie(&fiber.Cookie{Name: AppSessionCookieName, Value: value, Path: "/", HTTPOnly: true, Secure: m.secure, SameSite: fiber.CookieSameSiteLaxMode, MaxAge: int(m.ttl.Seconds())})
	return nil
}

func (m *AppSessionManager) Clear(c *fiber.Ctx) {
	secure := false
	if m != nil {
		secure = m.secure
	}
	c.Cookie(&fiber.Cookie{Name: AppSessionCookieName, Value: "", Path: "/", HTTPOnly: true, Secure: secure, SameSite: fiber.CookieSameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}
