package systemd_test

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/systemd"
)

func TestAvailableFalseWhenUnset(t *testing.T) {
	// No NOTIFY_SOCKET = no-op.
	t.Setenv("NOTIFY_SOCKET", "")
	if systemd.Available() {
		t.Error("Available should be false when NOTIFY_SOCKET unset")
	}
}

func TestNotifyNoopWithoutSocket(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if err := systemd.Notify("READY=1"); err != nil {
		t.Errorf("Notify returned error on no-op path: %v", err)
	}
}

// TestNotifyWritesToSocket creates a local unixgram socket + verifies the
// message arrives verbatim.
func TestNotifyWritesToSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "notify.sock")

	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = conn.Close() }()
	defer func() { _ = os.Remove(sock) }()

	t.Setenv("NOTIFY_SOCKET", sock)

	if notifyErr := systemd.Notify("READY=1"); notifyErr != nil {
		t.Fatalf("Notify: %v", notifyErr)
	}

	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 64)
	n, _, err := conn.ReadFromUnix(buf)
	if err != nil {
		t.Fatalf("ReadFromUnix: %v", err)
	}
	if got := string(buf[:n]); got != "READY=1" {
		t.Errorf("got %q, want %q", got, "READY=1")
	}
}

func TestWatchdogInterval(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "30000000") // 30s
	t.Setenv("WATCHDOG_PID", "")
	if got := systemd.WatchdogInterval(); got != 15*time.Second {
		t.Errorf("WatchdogInterval = %v, want 15s (half)", got)
	}
}

func TestWatchdogIntervalUnsetIsZero(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "")
	if got := systemd.WatchdogInterval(); got != 0 {
		t.Errorf("WatchdogInterval = %v, want 0", got)
	}
}

func TestCheckSocketAddrSafe(t *testing.T) {
	t.Parallel()
	if !systemd.CheckSocketAddrSafe("/run/systemd/notify") {
		t.Error("regular path should be safe")
	}
	if systemd.CheckSocketAddrSafe("@abstract-socket") && strings.HasPrefix("@abstract-socket", "@") {
		t.Error("abstract socket should not be safe")
	}
}
