package sockmap_test

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/pkg/sockmap"
)

// writeConfig writes a YAML config file into a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// newConfig builds a watch-disabled *config.Config from a YAML body.
func newConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	cfg, err := config.New(config.Options{
		Files: []string{writeConfig(t, body)},
		Watch: false,
	})
	require.NoError(t, err)
	return cfg
}

// bootModule boots sockmap.Module against cfg with a private Prometheus
// registry (so parallel tests don't collide on the default registerer) and
// returns the provided *Sockmap. The fx app is started + stopped via Cleanup.
func bootModule(t *testing.T, cfg *config.Config) *sockmap.Sockmap {
	t.Helper()
	var got *sockmap.Sockmap
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(prometheus.NewRegistry),
		sockmap.Module,
		fx.Populate(&got),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, app.Start(ctx))
	t.Cleanup(func() { require.NoError(t, app.Stop(ctx)) })
	return got
}

// TestModule_DisabledIsNoop boots the module with the default (disabled)
// config; Start must succeed on every platform and RegisterConn must not error.
func TestModule_DisabledIsNoop(t *testing.T) {
	t.Parallel()
	s := bootModule(t, newConfig(t, "sockmap:\n  enabled: false\n"))
	require.NotNil(t, s)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	conn, err := net.Dial("tcp", ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	// Disabled module never attaches; registering is a recorded no-op.
	require.NoError(t, s.RegisterConn(conn.(*net.TCPConn)))
}

// TestModule_EmptyConfigBoots verifies a totally absent sockmap section still
// boots (disabled by default).
func TestModule_EmptyConfigBoots(t *testing.T) {
	t.Parallel()
	s := bootModule(t, newConfig(t, "other:\n  key: value\n"))
	require.NotNil(t, s)
}

// TestRegisterConn_NilRejected guards the nil-conn path.
func TestRegisterConn_NilRejected(t *testing.T) {
	t.Parallel()
	s := bootModule(t, newConfig(t, "sockmap:\n  enabled: false\n"))
	require.Error(t, s.RegisterConn(nil))
}

// TestBytesProvider returns its fixed bytes.
func TestBytesProvider(t *testing.T) {
	t.Parallel()
	data := []byte("fake elf bytes")
	p := sockmap.BytesProvider(data)
	got, err := p()
	require.NoError(t, err)
	require.Equal(t, data, got)
}

// TestActivationListeners_NotActivated returns no listeners when LISTEN_PID
// is unset.
func TestActivationListeners_NotActivated(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	lns, names, err := sockmap.ActivationListeners()
	require.NoError(t, err)
	require.Nil(t, lns)
	require.Nil(t, names)
}

// TestActivationListeners_OtherPID ignores FDs addressed to a different PID.
func TestActivationListeners_OtherPID(t *testing.T) {
	t.Setenv("LISTEN_PID", "1")
	t.Setenv("LISTEN_FDS", "1")
	lns, _, err := sockmap.ActivationListeners()
	require.NoError(t, err)
	require.Nil(t, lns)
}

// TestActivationListeners_BadPID surfaces a parse error.
func TestActivationListeners_BadPID(t *testing.T) {
	t.Setenv("LISTEN_PID", "not-a-number")
	_, _, err := sockmap.ActivationListeners()
	require.Error(t, err)
}

// TestActivationListeners_ZeroFDs handles the activated-but-no-FDs case.
func TestActivationListeners_ZeroFDs(t *testing.T) {
	t.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("LISTEN_FDS", "0")
	lns, _, err := sockmap.ActivationListeners()
	require.NoError(t, err)
	require.Nil(t, lns)
}

// TestActivationListeners_BadFDs surfaces a malformed LISTEN_FDS.
func TestActivationListeners_BadFDs(t *testing.T) {
	t.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("LISTEN_FDS", "xyz")
	_, _, err := sockmap.ActivationListeners()
	require.Error(t, err)
}
