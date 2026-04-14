package systemd

import (
	"testing"
)

func TestAvailable_unset(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	if Available() {
		t.Fatal("want false when NOTIFY_SOCKET is unset")
	}
}

func TestAvailable_set(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "/run/systemd/notify")
	if !Available() {
		t.Fatal("want true when NOTIFY_SOCKET is set")
	}
}

func TestCheckSocketAddrSafe_abstract(t *testing.T) {
	t.Parallel()
	if CheckSocketAddrSafe("@/tmp/sock") {
		t.Fatal("want false for abstract socket address")
	}
}

func TestCheckSocketAddrSafe_normal(t *testing.T) {
	t.Parallel()
	if !CheckSocketAddrSafe("/tmp/sock") {
		t.Fatal("want true for normal socket address")
	}
}

func TestWatchdogInterval_unset(t *testing.T) {
	t.Setenv("WATCHDOG_USEC", "")
	if got := WatchdogInterval(); got != 0 {
		t.Fatalf("want 0 when WATCHDOG_USEC is unset, got %v", got)
	}
}
