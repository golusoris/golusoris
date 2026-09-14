// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package scan_test

import (
	"testing"
)

// TestModule_ClamdPingOnStart wires the clamd backend against a fake clamd
// server and confirms the boot-time ping succeeds.
func TestModule_ClamdPingOnStart(t *testing.T) {
	t.Parallel()
	srv := startFakeClamd(t, fakeConfig{pingReply: "PONG"})
	cfg := newConfig(t, "storage:\n  scan:\n    backend: clamd\n    address: "+srv.addr()+"\n")
	if _, err := bootScanner(t, cfg); err != nil {
		t.Fatalf("boot(clamd ping ok): %v", err)
	}
}

// TestModule_ClamdFailClosed confirms an unreachable daemon fails startup when
// fail_open is false (the security default).
func TestModule_ClamdFailClosed(t *testing.T) {
	t.Parallel()
	cfg := newConfig(t,
		"storage:\n  scan:\n    backend: clamd\n    address: 127.0.0.1:1\n    conn_timeout: 200ms\n")
	if _, err := bootScanner(t, cfg); err == nil {
		t.Fatal("boot(clamd unreachable, fail_open=false) = nil, want startup failure")
	}
}

// TestModule_ClamdFailOpen confirms an unreachable daemon does NOT fail startup
// when fail_open is true — it degrades with a warning instead.
func TestModule_ClamdFailOpen(t *testing.T) {
	t.Parallel()
	cfg := newConfig(t,
		"storage:\n  scan:\n    backend: clamd\n    address: 127.0.0.1:1\n"+
			"    conn_timeout: 200ms\n    fail_open: true\n")
	if _, err := bootScanner(t, cfg); err != nil {
		t.Fatalf("boot(clamd unreachable, fail_open=true) = %v, want nil", err)
	}
}

// TestModule_PingDisabled confirms ping_on_start=false skips the boot probe so
// an unreachable daemon does not fail startup.
func TestModule_PingDisabled(t *testing.T) {
	t.Parallel()
	cfg := newConfig(t,
		"storage:\n  scan:\n    backend: clamd\n    address: 127.0.0.1:1\n    ping_on_start: false\n")
	if _, err := bootScanner(t, cfg); err != nil {
		t.Fatalf("boot(ping disabled) = %v, want nil", err)
	}
}
