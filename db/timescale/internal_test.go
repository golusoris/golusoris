package timescale

import (
	"testing"
	"time"
)

func TestFormatInterval_hours(t *testing.T) {
	t.Parallel()
	got := formatInterval(2 * time.Hour)
	want := "2 hours"
	if got != want {
		t.Errorf("formatInterval(2h) = %q, want %q", got, want)
	}
}

func TestFormatInterval_minutes(t *testing.T) {
	t.Parallel()
	// 90 minutes = 1.5 hours; 1 hour in integer, not divisible by 24
	got := formatInterval(90 * time.Minute)
	want := "1 hours"
	if got != want {
		t.Errorf("formatInterval(90m) = %q, want %q", got, want)
	}
}

func TestFormatInterval_days(t *testing.T) {
	t.Parallel()
	got := formatInterval(30 * 24 * time.Hour)
	want := "30 days"
	if got != want {
		t.Errorf("formatInterval(30d) = %q, want %q", got, want)
	}
}
