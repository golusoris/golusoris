package scan_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/storage/scan"
)

// writeConfig writes a YAML config file into a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func newConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	cfg, err := config.New(config.Options{Files: []string{writeConfig(t, body)}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

// bootScanner boots scan.Module against cfg and returns the provided Scanner.
func bootScanner(t *testing.T, cfg *config.Config) (scan.Scanner, error) {
	t.Helper()
	var got scan.Scanner
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		scan.Module,
		fx.Populate(&got),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = app.Stop(stopCtx)
	})
	return got, nil
}

// TestModule_NoopBackend wires the noop backend (no daemon needed) and confirms
// it provides a working Scanner.
func TestModule_NoopBackend(t *testing.T) {
	t.Parallel()
	cfg := newConfig(t, "storage:\n  scan:\n    backend: noop\n")
	s, err := bootScanner(t, cfg)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("noop Ping: %v", err)
	}
}

// TestModule_UnknownBackend confirms an unknown backend fails wiring.
func TestModule_UnknownBackend(t *testing.T) {
	t.Parallel()
	cfg := newConfig(t, "storage:\n  scan:\n    backend: bogus\n")
	if _, err := bootScanner(t, cfg); err == nil {
		t.Fatal("boot(unknown backend) = nil error, want error")
	}
}

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

// TestModule_BadConfig confirms a malformed config value (bad duration) fails
// loadOptions and thus wiring.
func TestModule_BadConfig(t *testing.T) {
	t.Parallel()
	cfg := newConfig(t, "storage:\n  scan:\n    backend: noop\n    conn_timeout: not-a-duration\n")
	if _, err := bootScanner(t, cfg); err == nil {
		t.Fatal("boot(bad conn_timeout) = nil error, want error")
	}
}

// TestModule_BadMaxSize confirms an unparseable max_size fails clamd wiring.
func TestModule_BadMaxSize(t *testing.T) {
	t.Parallel()
	cfg := newConfig(t,
		"storage:\n  scan:\n    backend: clamd\n    max_size: not-a-size\n    ping_on_start: false\n")
	if _, err := bootScanner(t, cfg); err == nil {
		t.Fatal("boot(bad max_size) = nil error, want error")
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
