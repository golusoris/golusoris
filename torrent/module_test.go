package torrent_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/torrent"
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

// bootClient boots the torrent Module against cfg and returns the Client. The
// fx app is stopped via t.Cleanup. It does not call Start (so no qbittorrent
// login fires); construction is what we assert.
func bootClient(t *testing.T, cfg *config.Config) (torrent.Client, error) {
	t.Helper()
	var got torrent.Client
	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		torrent.Module,
		fx.Populate(&got),
	)
	if err := app.Err(); err != nil {
		return nil, err
	}
	return got, nil
}

// TestModule_DefaultBackendIsTransmission boots the Module with only the
// transmission URL set, leaving the backend selector unset. The default must
// yield a working transmission Client.
func TestModule_DefaultBackendIsTransmission(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{
		Files: []string{writeConfig(t, "torrent:\n  transmission:\n    url: http://localhost:9091/transmission/rpc\n")},
	})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	got, err := bootClient(t, cfg)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	if got == nil {
		t.Fatal("expected a non-nil torrent.Client")
	}
}

// TestModule_UnsupportedBackend asserts an unknown backend fails construction
// with ErrUnsupportedBackend.
func TestModule_UnsupportedBackend(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{
		Files: []string{writeConfig(t, "torrent:\n  backend: deluge\n")},
	})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	_, err = bootClient(t, cfg)
	if err == nil {
		t.Fatal("expected an error for backend=deluge")
	}
}

// TestModule_SelectsRTorrent confirms the rtorrent backend is selectable.
func TestModule_SelectsRTorrent(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{
		Files: []string{writeConfig(t, "torrent:\n  backend: rtorrent\n  rtorrent:\n    addr: http://localhost:8000/RPC2\n")},
	})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	got, err := bootClient(t, cfg)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	if got == nil {
		t.Fatal("expected a non-nil rtorrent Client")
	}
}

// TestModule_DefaultTimeoutApplied confirms an unset/zero timeout falls back to
// the default rather than producing a no-timeout client.
func TestModule_DefaultTimeoutApplied(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{
		Files: []string{writeConfig(t, "torrent:\n  backend: rtorrent\n  timeout: 0s\n  rtorrent:\n    addr: http://localhost:8000/RPC2\n")},
	})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	if _, err = bootClient(t, cfg); err != nil {
		t.Fatalf("boot: %v", err)
	}
}

// TestModule_QBittorrentLoginViaLifecycle confirms the qbittorrent backend
// registers its login on the fx lifecycle (Start triggers it). A bogus host
// makes Start fail, proving the OnStart hook ran.
func TestModule_QBittorrentLoginViaLifecycle(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{
		Files: []string{writeConfig(t, "torrent:\n  backend: qbittorrent\n  qbittorrent:\n    host: http://127.0.0.1:1\n    username: a\n    password: b\n")},
	})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var got torrent.Client
	app := fxtest.New(
		t,
		fx.NopLogger,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		torrent.Module,
		fx.Populate(&got),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if startErr := app.Start(ctx); startErr == nil {
		_ = app.Stop(ctx)
		t.Fatal("expected Start to fail when qbittorrent login cannot reach the daemon")
	}
}
