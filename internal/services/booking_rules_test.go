package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"office-craft-api/internal/apperror"
	"office-craft-api/internal/models"
)

type stubProofBooking struct{ booking models.Booking }

func (s stubProofBooking) GetByID(context.Context, string) (*models.Booking, error) {
	return &s.booking, nil
}

func TestLateBookingCanStillUploadProofs(t *testing.T) {
	past := time.Now().AddDate(0, 0, -10)
	future := time.Now().AddDate(0, 0, 3)
	cases := []struct {
		name   string
		status string
		start  time.Time
		kind   string
		ok     bool
	}{
		{"late before photo", models.BookingStatusApproved, past, models.ProofKindBefore, true},
		{"late after photo", models.BookingStatusInUse, past, models.ProofKindAfter, true},
		{"revision after photo", models.BookingStatusNeedsRevision, past, models.ProofKindAfter, true},
		{"before photo too early", models.BookingStatusApproved, future, models.ProofKindBefore, false},
		{"after photo needs started booking", models.BookingStatusApproved, past, models.ProofKindAfter, false},
		{"closed booking stays closed", models.BookingStatusClosed, past, models.ProofKindBefore, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := models.Booking{ID: "b1", Status: c.status, StartTime: c.start, EndTime: c.start.Add(time.Hour)}
			_, err := NewProofService(stubProofBooking{b}).ValidateUpload(context.Background(), "b1", c.kind)
			if (err == nil) != c.ok {
				t.Fatalf("ok=%v, err=%v", c.ok, err)
			}
		})
	}
}

func TestBookingRuleErrorsNameTheBlocker(t *testing.T) {
	var appErr *apperror.AppError

	err := resourceInUseError("actor", "holder", "Budi")
	if !errors.As(err, &appErr) || appErr.Code != "RESOURCE_IN_USE" || !strings.Contains(appErr.Message, "Budi") {
		t.Fatalf("unexpected resource error: %v", err)
	}
	if appErr.Details.(models.BookingRuleDetail).IsSelf {
		t.Fatal("another user's usage must not be marked as self")
	}

	err = unfinishedBookingError("owner", "owner", "Sari", []string{"Proyektor"})
	if !errors.As(err, &appErr) || appErr.Code != "USER_HAS_UNFINISHED_BOOKING" || !strings.Contains(appErr.Message, "Proyektor") {
		t.Fatalf("unexpected unfinished error: %v", err)
	}
	if !appErr.Details.(models.BookingRuleDetail).IsSelf {
		t.Fatal("own booking must be marked as self")
	}
}

func TestNotificationLinks(t *testing.T) {
	b := models.Booking{ID: "b1", StartTime: time.Now(), EndTime: time.Now().Add(time.Hour)}
	const site = "https://silapet.example"

	_, _, admin := buildAdminNotificationContent("requested", "Budi", "Proyektor", b, "", site)
	if !strings.Contains(admin, site+"/admin/bookings/b1") {
		t.Fatalf("admin request has no review link: %s", admin)
	}
	for _, kind := range []string{models.BookingStatusApproved, models.BookingStatusRejected} {
		_, _, text := buildNotificationContent(kind, "Budi", "Proyektor", b, "", site)
		if !strings.Contains(text, site+"/my-bookings/b1") {
			t.Fatalf("%s has no booking link: %s", kind, text)
		}
	}
	if _, _, text := buildNotificationContent(models.BookingStatusApproved, "Budi", "Proyektor", b, "", ""); strings.Contains(text, "/my-bookings/") {
		t.Fatalf("missing app URL must not produce a link: %s", text)
	}
}

func TestOverdueReminderGroupsAndNumbers(t *testing.T) {
	phone := "628123"
	users := map[string]*models.AppUser{
		"u1": {ID: "u1", FullName: "Budi", Status: models.UserStatusApproved, Phone: &phone},
		"u2": {ID: "u2", FullName: "Sari", Status: models.UserStatusApproved},
		"u3": {ID: "u3", FullName: "Nonaktif", Status: models.UserStatusRejected},
	}
	resources := map[string]*models.Resource{"r1": {Name: "Proyektor"}, "r2": {Name: "Kamera"}}
	now := time.Now()
	bookings := []models.Booking{
		{ID: "b1", UserID: "u1", ResourceID: "r1", Status: models.BookingStatusInUse, StartTime: now, EndTime: now},
		{ID: "b2", UserID: "u1", ResourceID: "r2", Status: models.BookingStatusApproved, StartTime: now, EndTime: now},
		{ID: "b3", UserID: "u2", ResourceID: "r1", Status: models.BookingStatusNeedsRevision, StartTime: now, EndTime: now},
		{ID: "b4", UserID: "u3", ResourceID: "r1", Status: models.BookingStatusInUse, StartTime: now, EndTime: now},
	}

	recipients, summary := groupOverdue(bookings, users, resources)
	if summary != (models.OverdueSummary{Bookings: 3, Users: 2, WithoutPhone: 1}) {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	_, _, many := buildOverdueReminder("Budi", recipients[0].items, "https://silapet.example")
	for _, want := range []string{"1. *Proyektor*", "2. *Kamera*", "/my-bookings/b1", "/my-bookings/b2", "batalkan"} {
		if !strings.Contains(many, want) {
			t.Fatalf("reminder misses %q: %s", want, many)
		}
	}

	_, _, one := buildOverdueReminder("Sari", recipients[1].items, "https://silapet.example")
	if strings.Contains(one, "1.") || !strings.Contains(one, "/my-bookings/b3") || strings.Contains(one, "batalkan") {
		t.Fatalf("single reminder is wrong: %s", one)
	}
}
