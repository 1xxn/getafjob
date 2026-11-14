package app

import (
	"testing"
	"time"
)

func TestClockUsesTheInstanceZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata:", err)
	}
	until := time.Date(2026, 11, 14, 13, 30, 0, 0, time.UTC)
	if got := clock(until, berlin); got != "14:30 CET" {
		t.Fatalf("clock = %q, want 14:30 CET", got)
	}
	if got := clock(until, time.UTC); got != "13:30 UTC" {
		t.Fatalf("clock = %q, want 13:30 UTC", got)
	}
}
