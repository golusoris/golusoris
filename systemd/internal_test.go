// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package systemd

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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

// listenNotifySocket binds a unixgram socket that never reads.
func listenNotifySocket(t *testing.T) (*net.UnixConn, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("blocking unixgram writes on a full receive queue are Linux kernel behavior")
	}
	sock := filepath.Join(t.TempDir(), "notify.sock")
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, sock
}

// fillReceiveQueue writes datagrams from fresh senders until a fresh sender
// cannot enqueue even one, so the receiver's queue is full.
func fillReceiveQueue(t *testing.T, sock string) {
	t.Helper()
	const maxSenders = 64
	for range maxSenders {
		if sendUntilBlocked(t, sock) == 0 {
			return
		}
	}
	t.Fatalf("receive queue still accepting after %d senders", maxSenders)
}

// sendUntilBlocked writes 1-byte datagrams until one blocks past a short
// deadline and returns how many were enqueued.
func sendUntilBlocked(t *testing.T, sock string) int {
	t.Helper()
	var dialer net.Dialer
	conn, err := dialer.DialContext(t.Context(), "unixgram", sock)
	if err != nil {
		t.Fatalf("dial filler: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	const maxWrites = 1 << 16
	for sent := range maxWrites {
		if derr := conn.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); derr != nil {
			t.Fatalf("set filler deadline: %v", derr)
		}
		if _, werr := conn.Write([]byte{'x'}); werr != nil {
			if !errors.Is(werr, os.ErrDeadlineExceeded) {
				t.Fatalf("filler write: %v", werr)
			}
			return sent
		}
	}
	t.Fatalf("filler wrote %d datagrams without blocking", maxWrites)
	return 0
}

func TestNotify_boundedContextDelivers(t *testing.T) {
	conn, sock := listenNotifySocket(t)
	t.Setenv("NOTIFY_SOCKET", sock)
	ctx, cancel := context.WithTimeout(t.Context(), notifyTimeout)
	defer cancel()
	if err := notify(ctx, "READY=1"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 16)
	n, _, err := conn.ReadFromUnix(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(buf[:n]); got != "READY=1" {
		t.Fatalf("got %q, want READY=1", got)
	}
}

func TestNotify_expiredContextFailsDial(t *testing.T) {
	_, sock := listenNotifySocket(t)
	t.Setenv("NOTIFY_SOCKET", sock)
	ctx, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	err := notify(ctx, "READY=1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("notify error = %v, want context.DeadlineExceeded", err)
	}
}

func TestNotify_fullQueueWriteHonorsDeadline(t *testing.T) {
	_, sock := listenNotifySocket(t)
	t.Setenv("NOTIFY_SOCKET", sock)
	fillReceiveQueue(t, sock)
	const budget = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), budget)
	defer cancel()
	start := time.Now()
	err := notify(ctx, "WATCHDOG=1")
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("notify error = %v, want os.ErrDeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 10*budget {
		t.Fatalf("notify returned after %v, want about %v", elapsed, budget)
	}
}

func TestNotify_unsetSocketIgnoresExpiredContext(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	ctx, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	if err := notify(ctx, "READY=1"); err != nil {
		t.Fatalf("notify without socket = %v, want nil", err)
	}
}

func TestPet_cancelledWatchdogAbortsDial(t *testing.T) {
	_, sock := listenNotifySocket(t)
	t.Setenv("NOTIFY_SOCKET", sock)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := pet(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("pet error = %v, want context.Canceled", err)
	}
}
