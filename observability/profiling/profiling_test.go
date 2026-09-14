// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package profiling_test

import (
	"testing"

	"github.com/golusoris/golusoris/observability/profiling"
)

func TestDisabledIsNoop(t *testing.T) {
	t.Parallel()
	p, err := profiling.Start(profiling.Options{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if p != nil {
		t.Error("expected nil profiler when disabled")
	}
}

func TestDefaultServer(t *testing.T) {
	t.Parallel()
	if got := profiling.DefaultOptions().Server; got != "http://pyroscope:4040" {
		t.Errorf("Server = %q", got)
	}
}
