package cron_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/jobs/cron"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		expr string
		ok   bool
	}{
		{"0 */5 * * *", true},
		{"@hourly", true},
		{"@every 30s", true},
		{"*/5 * * * * *", false}, // 6-field seconds variant — not enabled in our parser
		{"not-a-cron", false},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			err := cron.Validate(tc.expr)
			if tc.ok && err != nil {
				t.Errorf("Validate(%q) = %v, want nil", tc.expr, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("Validate(%q) = nil, want error", tc.expr)
			}
		})
	}
}

func TestScheduleNext(t *testing.T) {
	t.Parallel()
	s, err := cron.Schedule("0 * * * *") // top of every hour
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	now := time.Date(2026, 1, 1, 14, 30, 0, 0, time.UTC)
	next := s.Next(now)
	want := time.Date(2026, 1, 1, 15, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("next = %v, want %v", next, want)
	}
}

func TestValidateErrorMessage(t *testing.T) {
	t.Parallel()
	err := cron.Validate("bad")
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("err = %v, want parse error", err)
	}
}
