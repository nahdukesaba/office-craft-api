package services

import (
	"fmt"
	"log"

	"github.com/wneessen/go-mail"

	"office-craft-api/internal/config"
)

// EmailService sends transactional emails via SMTP. If SMTPHost isn't
// configured, it degrades to logging what it would have sent - the rest of
// the app (and the notify feature built on top of it) keeps working
// without email, useful while you're still setting up SMTP credentials.
type EmailService struct {
	cfg     *config.Config
	enabled bool
}

func NewEmailService(cfg *config.Config) *EmailService {
	return &EmailService{cfg: cfg, enabled: cfg.SMTPHost != "" && cfg.SMTPFromEmail != ""}
}

// Send delivers a single HTML email (with an auto-generated plain-text
// fallback part) to one recipient.
func (s *EmailService) Send(toEmail, toName, subject, htmlBody string) error {
	if !s.enabled {
		log.Printf("email (SMTP not configured, not sent): to=%s subject=%q", toEmail, subject)
		return nil
	}

	msg := mail.NewMsg()
	if err := msg.FromFormat(s.cfg.SMTPFromName, s.cfg.SMTPFromEmail); err != nil {
		return fmt.Errorf("setting from address: %w", err)
	}
	if toName != "" {
		if err := msg.AddToFormat(toName, toEmail); err != nil {
			return fmt.Errorf("setting to address: %w", err)
		}
	} else {
		if err := msg.To(toEmail); err != nil {
			return fmt.Errorf("setting to address: %w", err)
		}
	}
	msg.Subject(subject)
	msg.SetBodyString(mail.TypeTextHTML, htmlBody)

	client, err := mail.NewClient(s.cfg.SMTPHost,
		mail.WithPort(s.cfg.SMTPPort),
		mail.WithSMTPAuth(mail.SMTPAuthPlain),
		mail.WithUsername(s.cfg.SMTPUsername),
		mail.WithPassword(s.cfg.SMTPPassword),
		mail.WithTLSPolicy(mail.TLSMandatory),
	)
	if err != nil {
		return fmt.Errorf("creating SMTP client: %w", err)
	}

	if err := client.DialAndSend(msg); err != nil {
		return fmt.Errorf("sending email via %s:%d: %w", s.cfg.SMTPHost, s.cfg.SMTPPort, err)
	}
	return nil
}
