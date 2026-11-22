package discordbot

import (
	"testing"
	"time"
)

func TestNotificationTimeReached(t *testing.T) {
	now := time.Date(2026, 6, 19, 7, 0, 0, 0, time.Local)

	reached, normalized, err := notificationTimeReached("7:00", now)
	if err != nil {
		t.Fatalf("notificationTimeReached() error = %v", err)
	}
	if !reached {
		t.Fatal("notificationTimeReached() = false, want true")
	}
	if normalized != "07:00" {
		t.Fatalf("normalized = %q, want %q", normalized, "07:00")
	}
}

func TestNotificationTimeNotReached(t *testing.T) {
	now := time.Date(2026, 6, 19, 6, 59, 0, 0, time.Local)

	reached, _, err := notificationTimeReached("07:00", now)
	if err != nil {
		t.Fatalf("notificationTimeReached() error = %v", err)
	}
	if reached {
		t.Fatal("notificationTimeReached() = true, want false")
	}
}

func TestNotificationTimeRejectsInvalidValue(t *testing.T) {
	if _, _, err := notificationTimeReached("25:00", time.Now()); err == nil {
		t.Fatal("notificationTimeReached() error = nil, want error")
	}
}
