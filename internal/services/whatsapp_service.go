package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"office-craft-api/internal/config"
)

// WhatsAppService sends text messages through a self-hosted OpenWA gateway
// (https://github.com/rmyndharis/OpenWA). If OpenWABaseURL isn't
// configured, it degrades to logging what it would have sent, same as
// EmailService - the notify feature keeps working without WhatsApp wired up.
type WhatsAppService struct {
	cfg     *config.Config
	client  *http.Client
	enabled bool
}

func NewWhatsAppService(cfg *config.Config) *WhatsAppService {
	return &WhatsAppService{
		cfg:     cfg,
		client:  &http.Client{Timeout: 15 * time.Second},
		enabled: cfg.OpenWABaseURL != "" && cfg.OpenWAAPIKey != "" && cfg.OpenWASessionID != "",
	}
}

var nonDigits = regexp.MustCompile(`\D`)

// toChatID converts a stored phone number (plain digits, international
// format, no leading '+' - e.g. "6281234567890") into OpenWA's expected
// chatId format ("6281234567890@c.us"). Any stray non-digit characters
// (spaces, dashes, a leading '+') are stripped defensively.
func toChatID(phone string) string {
	return nonDigits.ReplaceAllString(phone, "") + "@c.us"
}

// Send delivers a plain-text WhatsApp message to one phone number.
func (s *WhatsAppService) Send(ctx context.Context, phone, message string) error {
	if !s.enabled {
		return fmt.Errorf("whatsapp not configured (OPENWA_BASE_URL/OPENWA_API_KEY/OPENWA_SESSION_ID)")
	}
	if phone == "" {
		return fmt.Errorf("recipient has no phone number on file")
	}

	payload, err := json.Marshal(map[string]string{
		"chatId": toChatID(phone),
		"text":   message,
	})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api/sessions/%s/messages/send-text", s.cfg.OpenWABaseURL, s.cfg.OpenWASessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", s.cfg.OpenWAAPIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("calling OpenWA: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OpenWA returned %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}
