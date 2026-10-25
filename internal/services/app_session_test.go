package services

import (
	"encoding/base64"
	"testing"
	"time"

	"office-craft-api/internal/models"
)

func testAppSessionManager(t *testing.T) *AppSessionManager {
	t.Helper()
	manager, err := NewAppSessionManager(base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901")), false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestAppSessionRoundTripAndTamperRejection(t *testing.T) {
	manager := testAppSessionManager(t)
	value, err := manager.Encode(AppSessionData{AppUserID: "user-id", Role: "admin", Status: "approved", LoginSource: models.LoginSourceSSO, SID: "central-sid", AccessToken: "access", RefreshToken: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := manager.Decode(value)
	if err != nil || decoded.AppUserID != "user-id" || decoded.LoginSource != models.LoginSourceSSO || decoded.SID != "central-sid" {
		t.Fatalf("unexpected session: %#v %v", decoded, err)
	}
	if _, err := manager.Decode(value[:len(value)-1] + "x"); err == nil {
		t.Fatal("tampered session accepted")
	}
}

func TestDirectAppSessionCannotCarrySIDAndExpiredSessionFails(t *testing.T) {
	manager := testAppSessionManager(t)
	if _, err := manager.Encode(AppSessionData{AppUserID: "user-id", Role: models.RoleUser, Status: models.UserStatusApproved, LoginSource: models.LoginSourceSSO, AccessToken: "access", RefreshToken: "refresh"}); err == nil {
		t.Fatal("SSO session without trusted SID was accepted")
	}
	if _, err := manager.Encode(AppSessionData{AppUserID: "user-id", Role: models.RoleUser, Status: models.UserStatusApproved, LoginSource: models.LoginSourceDirect, SID: "forged", AccessToken: "access", RefreshToken: "refresh"}); err == nil {
		t.Fatal("direct session accepted SID")
	}
	value, err := manager.Encode(AppSessionData{AppUserID: "user-id", Role: models.RoleUser, Status: models.UserStatusApproved, LoginSource: models.LoginSourceDirect, AccessToken: "access", RefreshToken: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := manager.Decode(value); err == nil {
		t.Fatal("expired session accepted")
	}
}
