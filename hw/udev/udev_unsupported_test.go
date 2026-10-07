//go:build !linux || !cgo

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package udev

import (
	"errors"
	"testing"
)

func TestNewMonitorReportsUnsupported(t *testing.T) {
	t.Parallel()
	monitor, err := NewMonitor(t.Context())
	if monitor != nil {
		t.Fatalf("NewMonitor() monitor = %v, want nil", monitor)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("NewMonitor() error = %v, want %v", err, ErrUnsupported)
	}
}
