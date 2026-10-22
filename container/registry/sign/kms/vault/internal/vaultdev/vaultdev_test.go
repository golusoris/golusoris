// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package vaultdev

import (
	"errors"
	"net"
	"runtime"
	"testing"
)

// heldPort keeps a listener on a port for the test, so a server bind fails.
func heldPort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold port: %v", err)
	}
	t.Cleanup(func() { closeListener(t, l) })
	return l.Addr().(*net.TCPAddr).Port
}

func skipOffLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("host networking needs a Linux Docker engine, not %s", runtime.GOOS)
	}
}

func TestStartHostNetworkRetriesTakenPort(t *testing.T) {
	t.Parallel()
	skipOffLinux(t)
	taken := heldPort(t)
	calls := 0
	pick := func(t *testing.T) int {
		t.Helper()
		calls++
		if calls == 1 {
			return taken
		}
		return freePortPair(t)
	}
	srv, err := startHostNetwork(t, OpenBao, pick)
	if err != nil {
		t.Fatalf("start after a taken port: %v", err)
	}
	ready(t, srv)
	if calls != 2 {
		t.Fatalf("port picks = %d, want 2", calls)
	}
}

func TestStartHostNetworkGivesUpAfterTries(t *testing.T) {
	t.Parallel()
	skipOffLinux(t)
	taken := heldPort(t)
	calls := 0
	pick := func(t *testing.T) int {
		t.Helper()
		calls++
		return taken
	}
	_, err := startHostNetwork(t, OpenBao, pick)
	if !errors.Is(err, errPortTaken) {
		t.Fatalf("err = %v, want errPortTaken", err)
	}
	if calls != hostTries {
		t.Fatalf("port picks = %d, want hostTries %d", calls, hostTries)
	}
}
