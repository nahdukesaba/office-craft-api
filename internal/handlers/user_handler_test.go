package handlers

import (
	"testing"

	"office-craft-api/internal/models"
)

func TestCanTransitionUserStatus(t *testing.T) {
	tests := []struct {
		name    string
		current string
		target  string
		want    bool
	}{
		{name: "pending to approved", current: models.UserStatusPending, target: models.UserStatusApproved, want: true},
		{name: "pending to rejected", current: models.UserStatusPending, target: models.UserStatusRejected, want: true},
		{name: "rejected to approved", current: models.UserStatusRejected, target: models.UserStatusApproved, want: true},
		{name: "approved to rejected", current: models.UserStatusApproved, target: models.UserStatusRejected, want: true},
		{name: "approved to approved", current: models.UserStatusApproved, target: models.UserStatusApproved, want: false},
		{name: "rejected to rejected", current: models.UserStatusRejected, target: models.UserStatusRejected, want: false},
		{name: "disabled to approved", current: models.UserStatusDisabled, target: models.UserStatusApproved, want: false},
		{name: "unknown target", current: models.UserStatusPending, target: "unknown", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canTransitionUserStatus(test.current, test.target); got != test.want {
				t.Fatalf("canTransitionUserStatus(%q, %q) = %t, want %t", test.current, test.target, got, test.want)
			}
		})
	}
}
