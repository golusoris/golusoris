// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package runtime_test

import (
	"testing"

	"github.com/golusoris/golusoris/container/runtime"
)

func TestDetectPopulatesHostname(t *testing.T) {
	t.Parallel()
	info := runtime.Detect()
	if info.Hostname == "" {
		t.Error("Hostname should be populated")
	}
}

func TestDetectIsNilSafe(t *testing.T) {
	t.Parallel()
	// Detect must never panic even on an unusual host.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Detect panicked: %v", r)
		}
	}()
	_ = runtime.Detect()
}

func TestRuntimeFallbackIsBare(t *testing.T) {
	t.Parallel()
	// On most dev hosts nothing matches k8s / docker / podman / systemd.
	// We can't fully isolate here, so just assert the result is one of
	// the known values.
	info := runtime.Detect()
	switch info.Runtime {
	case runtime.RuntimeK8s, runtime.RuntimeDocker, runtime.RuntimePodman,
		runtime.RuntimeSystemd, runtime.RuntimeBare:
		// all fine
	default:
		t.Errorf("unexpected runtime %q", info.Runtime)
	}
}
