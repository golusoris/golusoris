// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sentry_test

import (
	"testing"
	"time"

	"github.com/golusoris/golusoris/observability/sentry"
)

func TestEmptyDSNIsNoop(t *testing.T) {
	t.Parallel()
	if err := sentry.Init(sentry.Options{}); err != nil {
		t.Errorf("Init empty DSN: %v", err)
	}
}

func TestBadDSNErrors(t *testing.T) {
	t.Parallel()
	err := sentry.Init(sentry.Options{DSN: "not-a-url"})
	if err == nil {
		t.Fatal("expected error for malformed DSN")
	}
}

func TestFlush(t *testing.T) {
	t.Parallel()
	// Sentry not initialized — Flush should return without panic.
	_ = sentry.Flush(10 * time.Millisecond)
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	o := sentry.DefaultOptions()
	if o.Sample.Rate != 1.0 {
		t.Errorf("Sample.Rate = %v", o.Sample.Rate)
	}
	if o.Sample.Traces != 0 {
		t.Errorf("Sample.Traces = %v", o.Sample.Traces)
	}
}
