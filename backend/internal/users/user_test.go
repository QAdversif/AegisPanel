// SPDX-License-Identifier: AGPL-3.0-or-later

package users

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestStatus_IsValid checks the closed enum.
func TestStatus_IsValid(t *testing.T) {
	cases := []struct {
		s    Status
		want bool
	}{
		{StatusActive, true},
		{StatusGrace, true},
		{StatusDisabled, true},
		{StatusExpired, true},
		{StatusDeleted, true},
		{Status("nope"), false},
		{Status(""), false},
		{Status("ACTIVE"), false}, // case-sensitive
	}
	for _, tc := range cases {
		if got := tc.s.IsValid(); got != tc.want {
			t.Errorf("Status(%q).IsValid() = %v, want %v", tc.s, got, tc.want)
		}
	}
}

// TestUser_IsValid exercises the cheap pre-flight
// check. Heavier validation (username format, etc.)
// lives in the Service.
func TestUser_IsValid(t *testing.T) {
	validID := uuid.New()
	cases := []struct {
		name string
		u    *User
		want bool
	}{
		{"nil", nil, false},
		{"empty-username", &User{Status: StatusActive}, false},
		{"bad-status", &User{Username: "alice", Status: "nope"}, false},
		{"negative-traffic-limit", &User{Username: "alice", Status: StatusActive, TrafficLimitBytes: -1}, false},
		{"negative-traffic-used", &User{Username: "alice", Status: StatusActive, TrafficUsedBytes: -1}, false},
		{"negative-device-limit", &User{Username: "alice", Status: StatusActive, DeviceLimit: -1}, false},
		{"valid", &User{ID: validID, Username: "alice", Status: StatusActive, SubToken: "tok"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.IsValid(); got != tc.want {
				t.Errorf("IsValid() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUser_String_RedactsSubToken is a smoke test for
// the debug helper. We do NOT log the sub_token
// (security boundary), so the String output should
// contain the ID and the username but NOT the token.
func TestUser_String_RedactsSubToken(t *testing.T) {
	u := &User{
		ID:       uuid.New(),
		Username: "alice",
		Status:   StatusActive,
		SubToken: "supersecrettoken-donotlog",
	}
	s := u.String()
	if !contains(s, "alice") {
		t.Errorf("String() = %q, want to contain username", s)
	}
	if contains(s, "supersecrettoken") {
		t.Errorf("String() = %q, must not contain sub_token", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// _ = time.Now is here so the import is kept even
// if the test is the only consumer in this file.
// Future tests in this file will use time.Time.
var _ = time.Now
