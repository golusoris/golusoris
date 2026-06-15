// Package sockmap is an opt-in fx module for colocated, zero-TCP-stack IPC
// via an eBPF SK_MSG / SOCK_OPS sockmap redirect.
//
// When two processes (e.g. a golusoris API server and a colocated SvelteKit
// node process) run on the same host/VM/pod, a SOCK_OPS program attached to
// their shared cgroup v2 inserts their listening sockets into a pinned
// BPF_MAP_TYPE_SOCKHASH; an SK_MSG program then redirects payloads directly
// between the peer socket buffers, bypassing the loopback TCP/IP stack. The
// approach is transparent to app code — userspace sees a normal socket.
//
// # Linux only
//
// The redirect machinery requires a Linux kernel >= 5.10 (CO-RE/BTF baseline)
// with a cgroup v2 unified hierarchy and CAP_BPF (or CAP_SYS_ADMIN). On
// non-Linux platforms — and on Linux when [Options.Enabled] is false — the
// module is a no-op: it wires cleanly but attaches nothing.
//
// # Ownership boundary
//
// golusoris owns the pinned sockhash and its own listen-FD lifecycle: it
// inserts its FD on Start and removes it on Stop *before* the socket closes,
// so the map never briefly redirects to a destroyed socket. An external
// loader (e.g. the sveltesentio @sveltesentio/ipc-sockmap package) becomes a
// client of the same pinned map rather than its sole owner.
//
// # BPF object
//
// The SOCK_OPS + SK_MSG programs are CO-RE objects supplied by the consumer
// via an [ObjectProvider] (mirroring the lower-level ebpf package), so this
// package carries no clang/bpf2go build dependency. With no provider wired,
// the module still manages the sockhash + systemd FD handoff + lifecycle
// cleanup; only the kernel-side program attach is skipped.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    sockmap.Module, // opt-in; no-op unless sockmap.enabled = true
//	    fx.Provide(func() sockmap.ObjectProvider {
//	        return sockmap.BytesProvider(bpfObjects) // CO-RE bytes from bpf2go
//	    }),
//	    fx.Invoke(func(m *sockmap.Sockmap, ln net.Listener) error {
//	        return m.RegisterListener(ln) // insert this FD into the sockhash
//	    }),
//	)
//
// Config keys live under the "sockmap" prefix.
package sockmap

import (
	"errors"
	"fmt"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// minKernelDefault is the CO-RE/BTF baseline the issue mandates (>= 5.10).
const (
	minKernelMajorDefault = 5
	minKernelMinorDefault = 10
)

// defaultPinPath is the bpffs location of the shared sockhash. Configurable
// so the consumer-side loader can agree on a path.
const defaultPinPath = "/sys/fs/bpf/golusoris/sockhash"

// defaultMapName is the kernel debug-aid name for the sockhash.
const defaultMapName = "golusoris_sockhash"

// defaultMaxEntries bounds the sockhash; one entry per registered socket.
const defaultMaxEntries = 1024

// defaultSockOpsProg / defaultSkMsgProg are the program names looked up in the
// supplied BPF collection. Overridable to match the consumer's object.
const (
	defaultSockOpsProg = "sockmap_sockops"
	defaultSkMsgProg   = "sockmap_redirect"
)

// Options selects and tunes the sockmap redirect. All knobs live under the
// "sockmap" config prefix.
type Options struct {
	// Enabled turns the module on. Default false — Tier 3 redirect is strictly
	// opt-in and must never change golusoris defaults.
	Enabled bool `koanf:"enabled"`

	// PinPath is the bpffs path of the shared sockhash (default
	// "/sys/fs/bpf/golusoris/sockhash"). The external loader reads the same
	// pin to become a client of the map.
	PinPath string `koanf:"pin_path"`

	// MapName is the kernel debug-aid name for the sockhash.
	MapName string `koanf:"map_name"`

	// MaxEntries bounds the sockhash (one slot per registered socket).
	MaxEntries uint32 `koanf:"max_entries"`

	// CgroupPath pins the cgroup v2 directory the SOCK_OPS program attaches
	// to. Empty means auto-detect the process's own cgroup v2 path from
	// /proc/self/cgroup. Set explicitly (e.g. via systemd Slice=) when the
	// attach point must be predictable.
	CgroupPath string `koanf:"cgroup_path"`

	// SockOpsProg / SkMsgProg name the programs to look up in the supplied
	// BPF collection.
	SockOpsProg string `koanf:"sockops_prog"`
	SkMsgProg   string `koanf:"skmsg_prog"`

	// MinKernelMajor / MinKernelMinor set the kernel-version floor. The
	// module refuses to load below this on Linux. Defaults to 5.10.
	MinKernelMajor int `koanf:"min_kernel_major"`
	MinKernelMinor int `koanf:"min_kernel_minor"`
}

func defaultOptions() Options {
	return Options{
		Enabled:        false,
		PinPath:        defaultPinPath,
		MapName:        defaultMapName,
		MaxEntries:     defaultMaxEntries,
		CgroupPath:     "",
		SockOpsProg:    defaultSockOpsProg,
		SkMsgProg:      defaultSkMsgProg,
		MinKernelMajor: minKernelMajorDefault,
		MinKernelMinor: minKernelMinorDefault,
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("sockmap", &opts); err != nil {
		return Options{}, fmt.Errorf("sockmap: load options: %w", err)
	}
	// A zeroed MaxEntries from a partial config would make the map useless;
	// restore the default rather than failing at kernel-load time.
	if opts.MaxEntries == 0 {
		opts.MaxEntries = defaultMaxEntries
	}
	if opts.PinPath == "" {
		opts.PinPath = defaultPinPath
	}
	if opts.MapName == "" {
		opts.MapName = defaultMapName
	}
	if opts.SockOpsProg == "" {
		opts.SockOpsProg = defaultSockOpsProg
	}
	if opts.SkMsgProg == "" {
		opts.SkMsgProg = defaultSkMsgProg
	}
	return opts, nil
}

// ObjectProvider returns the raw ELF bytes of a compiled, CO-RE-enabled eBPF
// object carrying the SOCK_OPS + SK_MSG programs. nil ObjectProvider is
// allowed: the module then manages the sockhash + FD lifecycle but skips the
// kernel-side program attach.
type ObjectProvider func() ([]byte, error)

// BytesProvider returns an [ObjectProvider] that serves fixed bytes (e.g. the
// embedded output of bpf2go).
func BytesProvider(data []byte) ObjectProvider {
	return func() ([]byte, error) { return data, nil }
}

// Metrics holds the Prometheus collectors the issue requires. They are read
// from BPF map stats on the Linux path; on the stub path they stay at zero.
type Metrics struct {
	// RedirectedBytes counts payload bytes redirected via the sockmap.
	RedirectedBytes prometheus.Counter
	// ActiveSockets gauges the number of sockets currently in the sockhash.
	ActiveSockets prometheus.Gauge
	// RedirectErrors counts redirect failures reported by the BPF program.
	RedirectErrors prometheus.Counter
}

// metricsParams pulls an optional *prometheus.Registry from the graph. When
// absent the collectors land on prometheus.DefaultRegisterer, matching
// k8s/metrics/prom's default.
type metricsParams struct {
	fx.In
	Registry *prometheus.Registry `optional:"true"`
}

// provideMetrics is the fx constructor for *Metrics.
func provideMetrics(p metricsParams) *Metrics {
	var reg prometheus.Registerer
	if p.Registry != nil {
		reg = p.Registry
	}
	return newMetrics(reg)
}

// newMetrics builds the collectors and registers them on reg. A nil reg falls
// back to the default registerer. Registration is tolerant of duplicates so
// repeated wiring (e.g. in tests) doesn't panic.
func newMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		RedirectedBytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "golusoris_sockmap_redirected_bytes_total",
			Help: "Total payload bytes redirected via the eBPF sockmap.",
		}),
		ActiveSockets: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "golusoris_sockmap_active_sockets",
			Help: "Sockets currently registered in the sockhash.",
		}),
		RedirectErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "golusoris_sockmap_redirect_errors_total",
			Help: "Total sockmap redirect errors reported by the BPF program.",
		}),
	}
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	register(reg, m.RedirectedBytes)
	register(reg, m.ActiveSockets)
	register(reg, m.RedirectErrors)
	return m
}

// register is MustRegister that tolerates AlreadyRegisteredError so the module
// is safe to wire more than once in a process (tests, multi-app embeds).
func register(reg prometheus.Registerer, c prometheus.Collector) {
	if err := reg.Register(c); err != nil {
		var are prometheus.AlreadyRegisteredError
		if !asAlreadyRegistered(err, &are) {
			panic(err)
		}
	}
}

func asAlreadyRegistered(err error, target *prometheus.AlreadyRegisteredError) bool {
	are, ok := err.(prometheus.AlreadyRegisteredError) //nolint:errorlint // prometheus returns the value, not a wrapped error
	if ok {
		*target = are
	}
	return ok
}

// RegisterConn inserts an established socket's FD into the sockhash so the
// SK_MSG program can redirect payloads to its peer. Called before fx Start it
// records the conn for insertion at Start; called after Start it inserts
// immediately. On non-Linux platforms it is a no-op.
//
// The argument is any [syscall.Conn] — a *net.TCPConn is the common case. Note
// the kernel only accepts *established* TCP sockets here: a listening socket
// returns EOPNOTSUPP, because the listen socket is meant to be entered into the
// sockhash by the SOCK_OPS program from kernel context (on the passive/active
// established callbacks), not from userspace. RegisterConn covers the case
// where the app already holds an accepted/dialed connection it wants redirected
// and lets golusoris own that entry's removal before close.
func (s *Sockmap) RegisterConn(c syscall.Conn) error {
	if c == nil {
		return errors.New("sockmap: register conn: nil conn")
	}
	return s.registerConn(c)
}

// Module provides *Sockmap + *Metrics and wires the sockhash load + FD
// insertion (Start) and the pre-shutdown cleanup hook (Stop) into the fx
// lifecycle. Opt-in: a no-op unless sockmap.enabled = true and the platform
// supports it.
var Module = fx.Module(
	"golusoris.sockmap",
	fx.Provide(loadOptions),
	fx.Provide(provideMetrics),
	fx.Provide(newSockmap),
	fx.Invoke(startSockmap),
)
