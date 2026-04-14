// Package systemd implements sd_notify + watchdog for processes run as
// systemd units. Zero-dep (talks to the NOTIFY_SOCKET unix datagram
// directly). No-op when NOTIFY_SOCKET is unset, so the module is safe to
// wire unconditionally.
//
// Typical usage:
//
//	fx.New(
//	    golusoris.Core,
//	    systemd.Module, // sends READY=1 on Start, WATCHDOG=1 on a ticker, STOPPING=1 on Stop
//	)
//
// The matching unit file enables sd_notify + watchdog:
//
//	[Service]
//	Type=notify
//	NotifyAccess=main
//	WatchdogSec=30s
//	Restart=on-failure
package systemd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
)

// Available reports whether the process is running under systemd with
// sd_notify enabled (Type=notify).
func Available() bool { return os.Getenv("NOTIFY_SOCKET") != "" }

// Notify sends a single sd_notify message. state is formatted as systemd
// expects — "READY=1", "RELOADING=1", "STOPPING=1", "STATUS=...", etc.
// Returns nil when NOTIFY_SOCKET is unset (no-op).
func Notify(state string) error {
	sock := os.Getenv("NOTIFY_SOCKET")
	if sock == "" {
		return nil
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("systemd: dial %s: %w", sock, err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(state)); err != nil {
		return fmt.Errorf("systemd: write: %w", err)
	}
	return nil
}

// WatchdogInterval returns half of WATCHDOG_USEC (the recommended pet
// interval), or 0 if the watchdog isn't enabled. Per systemd docs:
// "this will be the frequency which the service should send notifications
// to systemd, and should be less than the interval set here, preferably
// half of it".
func WatchdogInterval() time.Duration {
	v := os.Getenv("WATCHDOG_USEC")
	if v == "" {
		return 0
	}
	usec, err := strconv.ParseInt(v, 10, 64)
	if err != nil || usec <= 0 {
		return 0
	}
	pidEnv := os.Getenv("WATCHDOG_PID")
	if pidEnv != "" {
		pid, perr := strconv.Atoi(pidEnv)
		if perr == nil && pid != os.Getpid() {
			// Watchdog is for another PID (e.g. MainPID); skip.
			return 0
		}
	}
	return time.Duration(usec) * time.Microsecond / 2
}

// Ready notifies systemd that startup is complete.
func Ready() error { return Notify("READY=1") }

// Stopping notifies systemd that the process is shutting down.
func Stopping() error { return Notify("STOPPING=1") }

// Pet sends the watchdog heartbeat ("WATCHDOG=1").
func Pet() error { return Notify("WATCHDOG=1") }

// Module wires sd_notify into fx: READY=1 on Start, STOPPING=1 on Stop,
// and WATCHDOG=1 on a ticker honoring WATCHDOG_USEC. No-op when the
// process isn't running under systemd.
var Module = fx.Module("golusoris.systemd",
	fx.Invoke(func(lc fx.Lifecycle, clk clock.Clock, logger *slog.Logger) {
		if !Available() {
			return
		}
		watchdogCtx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		lc.Append(fx.Hook{
			OnStart: func(_ context.Context) error {
				if err := Ready(); err != nil {
					return err
				}
				logger.Info("systemd: READY=1 sent")
				go func() {
					defer close(done)
					runWatchdog(watchdogCtx, clk, logger)
				}()
				return nil
			},
			OnStop: func(_ context.Context) error {
				cancel()
				<-done
				if err := Stopping(); err != nil && !errors.Is(err, os.ErrNotExist) {
					logger.Warn("systemd: STOPPING=1 failed", slog.String("error", err.Error()))
				}
				return nil
			},
		})
	}),
)

// runWatchdog pings WATCHDOG=1 at the systemd-suggested interval.
// Returns when ctx is canceled. Failures are logged but don't abort —
// systemd will kill+restart us if pets stop, which is the desired
// failure mode.
func runWatchdog(ctx context.Context, clk clock.Clock, logger *slog.Logger) {
	interval := WatchdogInterval()
	if interval <= 0 {
		return
	}
	logger.InfoContext(ctx, "systemd: watchdog enabled", slog.Duration("interval", interval))
	for {
		select {
		case <-ctx.Done():
			return
		case <-clk.After(interval):
			if err := Pet(); err != nil {
				// Log at Warn; the next iteration retries. If systemd
				// actually doesn't hear from us it'll kill the unit —
				// that's the watchdog's job.
				logger.WarnContext(ctx, "systemd: watchdog pet failed", slog.String("error", err.Error()))
			}
		}
	}
}

// CheckSocketAddrSafe rejects the abstract-socket form ("@...") that
// the standard library's unixgram dialer doesn't handle on non-Linux.
// Unused in the happy path; exported for tests.
func CheckSocketAddrSafe(addr string) bool { return !strings.HasPrefix(addr, "@") }
