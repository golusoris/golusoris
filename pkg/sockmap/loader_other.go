//go:build !linux

package sockmap

import (
	"log/slog"
	"syscall"

	"go.uber.org/fx"
)

// Sockmap is the non-Linux stub. The eBPF SK_MSG redirect is Linux-only, so
// off-Linux the type wires cleanly but attaches nothing. RegisterConn records
// nothing and never errors, so callers compile and run identically across
// platforms.
type Sockmap struct {
	opts Options
	log  *slog.Logger
	m    *Metrics
}

// sockmapParams are the fx dependencies of the stub module.
type sockmapParams struct {
	fx.In
	Options  Options
	Metrics  *Metrics
	Logger   *slog.Logger
	Provider ObjectProvider `optional:"true"`
}

// newSockmap constructs the stub Sockmap.
func newSockmap(p sockmapParams) *Sockmap {
	return &Sockmap{opts: p.Options, log: p.Logger, m: p.Metrics}
}

// startSockmap logs that the redirect is unavailable on this platform and
// returns. The module stays wired so apps can compose it unconditionally.
func startSockmap(_ fx.Lifecycle, s *Sockmap) {
	if !s.opts.Enabled {
		return
	}
	s.log.Warn("sockmap: enabled but unavailable on this platform — eBPF SK_MSG redirect is Linux-only; running as no-op")
}

// registerConn is a no-op on non-Linux platforms.
func (s *Sockmap) registerConn(_ syscall.Conn) error { return nil }
