package services

import (
	"context"
	"fmt"
	"log"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
	"office-craft-api/internal/repository"
	"office-craft-api/internal/utils"
)

// notifiableStatuses gates the manual POST /bookings/:id/notify endpoint
// only - the internal NotifyForAction path (used for auto-notify on
// approve/reject/revoke) bypasses this, since "rejected" and "cancelled"
// (revoke's resulting status) aren't in this set but absolutely should
// still notify the owner.
var notifiableStatuses = map[string]bool{
	models.BookingStatusApproved:      true,
	models.BookingStatusInUse:         true,
	models.BookingStatusFinished:      true,
	models.BookingStatusNeedsRevision: true,
}

type NotifyService struct {
	bookings  *repository.BookingRepository
	users     *repository.UserRepository
	resources *repository.ResourceRepository
	email     *EmailService
	whatsapp  *WhatsAppService
}

func NewNotifyService(bookings *repository.BookingRepository, users *repository.UserRepository, resources *repository.ResourceRepository, email *EmailService, whatsapp *WhatsAppService) *NotifyService {
	return &NotifyService{bookings: bookings, users: users, resources: resources, email: email, whatsapp: whatsapp}
}

// NotifyResult reports what actually got sent, so the caller (and
// ultimately an admin looking at the response) can see at a glance whether
// a channel was skipped (e.g. no phone on file) rather than assuming full
// delivery just because the HTTP call returned 200.
type NotifyResult struct {
	Booking       *models.Booking `json:"booking"`
	EmailSent     bool            `json:"emailSent"`
	EmailError    string          `json:"emailError,omitempty"`
	WhatsAppSent  bool            `json:"whatsAppSent"`
	WhatsAppError string          `json:"whatsAppError,omitempty"`
}

// Notify is the manual, admin/user-triggered endpoint's entry point: sends
// a notification for the booking's *current* status, but only when that
// status is approved/in_use/finished.
func (s *NotifyService) Notify(ctx context.Context, bookingID, note string) (*NotifyResult, error) {
	booking, err := s.bookings.GetByID(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if booking == nil {
		return nil, apperror.NotFound("BOOKING_NOT_FOUND", "booking not found")
	}
	if !notifiableStatuses[booking.Status] {
		return nil, apperror.BadRequest("NOTIFY_NOT_ALLOWED", "notifications are only allowed for approved, in_use, finished, or needs_revision bookings")
	}

	owner, err := s.users.GetByID(ctx, booking.UserID)
	if err != nil {
		return nil, err
	}
	if owner == nil {
		return nil, apperror.NotFound("USER_NOT_FOUND", "booking owner not found")
	}

	return s.send(ctx, *booking, owner, s.resourceName(ctx, booking.ResourceID), booking.Status, note), nil
}

// NotifyForAction is the internal, best-effort entry point used by
// BookingService right after approve/reject/revoke succeed. Unlike Notify,
// it takes an explicit `kind` rather than deriving content from the
// booking's stored status - this matters because revoke's resulting status
// is "cancelled", identical to a plain self-cancel, so status alone can't
// distinguish "admin revoked this" from "user cancelled their own booking"
// (which intentionally does NOT notify). It never returns an error - the
// caller (an already-successful booking mutation) shouldn't fail or roll
// back because a notification couldn't be sent; problems are logged here
// and reflected in the returned result for anyone who wants to inspect it.
func (s *NotifyService) NotifyForAction(ctx context.Context, booking models.Booking, kind, note string) *NotifyResult {
	owner, err := s.users.GetByID(ctx, booking.UserID)
	if err != nil || owner == nil {
		log.Printf("notify: could not load owner for booking %s (kind=%s): %v", booking.ID, kind, err)
		return &NotifyResult{Booking: &booking}
	}
	return s.send(ctx, booking, owner, s.resourceName(ctx, booking.ResourceID), kind, note)
}

// NotifyAdminsForAction fans a notification out to every active admin -
// used for "new booking request" (on create), "started" (on start), and
// "finished" (on finish), so admins don't have to poll the dashboard to
// know a booking needs review or that an asset is now in use. Best-effort,
// like NotifyForAction: never returns an error, just logs problems.
func (s *NotifyService) NotifyAdminsForAction(ctx context.Context, booking models.Booking, kind, note string) {
	admins, err := s.users.ListAdmins(ctx)
	if err != nil {
		log.Printf("notify: could not list admins for booking %s (kind=%s): %v", booking.ID, kind, err)
		return
	}
	if len(admins) == 0 {
		return
	}

	ownerName := "A user"
	if owner, err := s.users.GetByID(ctx, booking.UserID); err == nil && owner != nil {
		ownerName = owner.FullName
	}
	resourceName := s.resourceName(ctx, booking.ResourceID)

	subject, htmlBody, textBody := buildAdminNotificationContent(kind, ownerName, resourceName, booking, note)

	for _, admin := range admins {
		if err := s.email.Send(admin.Email, admin.FullName, subject, htmlBody); err != nil {
			log.Printf("notify: admin email send failed (admin=%s, booking=%s, kind=%s): %v", admin.ID, booking.ID, kind, err)
		}
		phone := ""
		if admin.Phone != nil {
			phone = *admin.Phone
		}
		if err := s.whatsapp.Send(ctx, phone, textBody); err != nil {
			log.Printf("notify: admin whatsapp send failed (admin=%s, booking=%s, kind=%s): %v", admin.ID, booking.ID, kind, err)
		}
	}
}

func (s *NotifyService) resourceName(ctx context.Context, resourceID string) string {
	if res, err := s.resources.GetByID(ctx, resourceID); err == nil && res != nil {
		return res.Name
	}
	return "the resource"
}

func (s *NotifyService) send(ctx context.Context, booking models.Booking, owner *models.AppUser, resourceName, kind, note string) *NotifyResult {
	subject, htmlBody, textBody := buildNotificationContent(kind, owner.FullName, resourceName, booking, note)

	result := &NotifyResult{Booking: &booking}

	if err := s.email.Send(owner.Email, owner.FullName, subject, htmlBody); err != nil {
		log.Printf("notify: email send failed for booking %s (kind=%s): %v", booking.ID, kind, err)
		result.EmailError = err.Error()
	} else {
		result.EmailSent = true
	}

	phone := ""
	if owner.Phone != nil {
		phone = *owner.Phone
	}
	if err := s.whatsapp.Send(ctx, phone, textBody); err != nil {
		log.Printf("notify: whatsapp send failed for booking %s (kind=%s): %v", booking.ID, kind, err)
		result.WhatsAppError = err.Error()
	} else {
		result.WhatsAppSent = true
	}

	return result
}

// buildNotificationContent returns (subject, htmlBody, textBody) for the
// given notification kind ("approved", "rejected", "revoked", "in_use",
// "finished"). textBody doubles as the WhatsApp message (plain text);
// htmlBody is the email body. Both carry the same substance.
func buildNotificationContent(kind, ownerName, resourceName string, booking models.Booking, note string) (string, string, string) {
	timeRange := formatBookingRange(booking)

	var subject, mainText string
	switch kind {
	case models.BookingStatusApproved:
		subject = fmt.Sprintf("Booking Approved: %s", resourceName)
		mainText = fmt.Sprintf(
			"Hi %s, your booking for *%s* (%s) has been approved.\n\nPlease use the asset responsibly. Don't forget to upload the BEFORE photo when you start using it, and the AFTER photo once you're done.",
			ownerName, resourceName, timeRange,
		)
	case models.BookingStatusInUse:
		subject = fmt.Sprintf("Reminder: %s is currently in use", resourceName)
		mainText = fmt.Sprintf(
			"Hi %s, this is a reminder for your ongoing booking of *%s* (%s).\n\nPlease upload the AFTER proof photo once you're done, and be mindful of the time - another booking may be waiting for this asset right after yours.",
			ownerName, resourceName, timeRange,
		)
	case models.BookingStatusFinished:
		subject = fmt.Sprintf("Thank you for using %s", resourceName)
		mainText = fmt.Sprintf(
			"Hi %s, thank you for using *%s* (%s) responsibly! Your booking has been marked as finished.",
			ownerName, resourceName, timeRange,
		)
	case models.BookingStatusRejected:
		subject = fmt.Sprintf("Booking Rejected: %s", resourceName)
		mainText = fmt.Sprintf(
			"Hi %s, unfortunately your booking request for *%s* (%s) has been rejected.",
			ownerName, resourceName, timeRange,
		)
	case "revoked":
		subject = fmt.Sprintf("Booking Cancelled by Admin: %s", resourceName)
		mainText = fmt.Sprintf(
			"Hi %s, your approved booking for *%s* (%s) has been cancelled by an admin.",
			ownerName, resourceName, timeRange,
		)
	default:
		subject = fmt.Sprintf("Update on your booking of %s", resourceName)
		mainText = fmt.Sprintf("Hi %s, your booking of *%s* (%s) status is now: %s.", ownerName, resourceName, timeRange, kind)
	}

	if note != "" {
		mainText += fmt.Sprintf("\n\nNote from admin: %s", note)
	}

	textBody := mainText // WhatsApp: plain text, *asterisks* render as bold in the WhatsApp client itself.
	htmlBody := "<p>" + htmlEscapeNewlines(mainText) + "</p>"

	return subject, htmlBody, textBody
}

// buildAdminNotificationContent is the admin-facing counterpart to
// buildNotificationContent - same signature shape, different audience and
// copy. kind is one of "requested" (new pending booking), "started", or
// "finished".
func buildAdminNotificationContent(kind, ownerName, resourceName string, booking models.Booking, note string) (string, string, string) {
	timeRange := formatBookingRange(booking)

	var subject, mainText string
	switch kind {
	case "requested":
		subject = fmt.Sprintf("New Booking Request: %s", resourceName)
		mainText = fmt.Sprintf(
			"%s requested *%s* (%s). Please review and approve or reject it.",
			ownerName, resourceName, timeRange,
		)
	case "started":
		subject = fmt.Sprintf("Booking Started: %s", resourceName)
		mainText = fmt.Sprintf(
			"%s has started using *%s* (%s).",
			ownerName, resourceName, timeRange,
		)
	case "finished":
		subject = fmt.Sprintf("Booking Finished: %s", resourceName)
		mainText = fmt.Sprintf(
			"%s has finished using *%s* (%s). Please review the after-photo proof for any missing or damaged items.",
			ownerName, resourceName, timeRange,
		)
	default:
		subject = fmt.Sprintf("Booking Update: %s", resourceName)
		mainText = fmt.Sprintf("%s's booking of *%s* (%s) status is now: %s.", ownerName, resourceName, timeRange, kind)
	}

	if note != "" {
		mainText += fmt.Sprintf("\n\nNote: %s", note)
	}

	textBody := mainText
	htmlBody := "<p>" + htmlEscapeNewlines(mainText) + "</p>"

	return subject, htmlBody, textBody
}

func formatBookingRange(b models.Booking) string {
	loc := utils.JakartaLocation()
	start := b.StartTime.In(loc)
	end := b.EndTime.In(loc)
	if start.Format("2006-01-02") == end.Format("2006-01-02") {
		return fmt.Sprintf("%s, %s - %s WIB", start.Format("Jan 2, 2006"), start.Format("15:04"), end.Format("15:04"))
	}
	return fmt.Sprintf("%s - %s WIB", start.Format("Jan 2, 2006 15:04"), end.Format("Jan 2, 2006 15:04"))
}

// htmlEscapeNewlines is a tiny helper - real HTML escaping of user-supplied
// text (the admin note) plus turning \n into <br> for email rendering.
// Kept deliberately minimal since this is internal notification copy, not
// arbitrary untrusted HTML rendering.
func htmlEscapeNewlines(s string) string {
	escaped := ""
	for _, r := range s {
		switch r {
		case '<':
			escaped += "&lt;"
		case '>':
			escaped += "&gt;"
		case '&':
			escaped += "&amp;"
		case '\n':
			escaped += "<br>"
		default:
			escaped += string(r)
		}
	}
	return escaped
}
