// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cron_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/golusoris/golusoris/jobs"
	"github.com/golusoris/golusoris/jobs/cron"
)

type registerArgs struct{}

func (registerArgs) Kind() string { return "cron-register-probe" }

func TestRegisterRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	if err := cron.Register[registerArgs](nil, "@hourly", nil); err == nil || !strings.Contains(err.Error(), "constructor") {
		t.Fatalf("nil constructor error = %v", err)
	}
	if err := cron.Register(nil, "@hourly", func() registerArgs { return registerArgs{} }); err == nil || !strings.Contains(err.Error(), "client") {
		t.Fatalf("nil client error = %v", err)
	}
	var _ river.JobArgs = registerArgs{}
}

func TestRegisterRejectsProducerOnlyClient(t *testing.T) {
	t.Parallel()

	client, err := jobs.New(nil, jobs.DefaultOptions(), nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("new producer-only client: %v", err)
	}
	if err := cron.Register(client, "@hourly", func() registerArgs { return registerArgs{} }); err == nil || !strings.Contains(err.Error(), "periodic jobs") {
		t.Fatalf("producer-only registration error = %v", err)
	}
}

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
