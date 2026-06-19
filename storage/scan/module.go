package scan

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
)

// Backend identifiers.
const (
	backendClamd = "clamd"
	backendNoop  = "noop"
)

// startPingTimeout bounds the boot-time reachability probe so a wedged daemon
// can't stall fx startup indefinitely.
const startPingTimeout = 10 * time.Second

// Options selects and tunes the malware scanner. Config keys live under the
// "storage.scan" prefix.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    scan.Module, // provides scan.Scanner
//	)
type Options struct {
	// Backend selects the scanner backend: "clamd" (default) or "noop".
	Backend string `koanf:"backend"`
	// Address is host:port for tcp, or the socket path for unix.
	Address string `koanf:"address"`
	// Network is "tcp" (default) or "unix".
	Network string `koanf:"network"`
	// ConnTimeout is the dial timeout (clamd.SetConnTimeout).
	ConnTimeout time.Duration `koanf:"conn_timeout"`
	// CmdTimeout is the per-command timeout (clamd.SetCmdTimeout); scans can be
	// slow, so this is generous.
	CmdTimeout time.Duration `koanf:"cmd_timeout"`
	// ConnRetries is the dial retry count on timeout (clamd.SetConnRetries).
	ConnRetries int `koanf:"conn_retries"`
	// ConnSleep is the sleep between dial retries (clamd.SetConnSleep).
	ConnSleep time.Duration `koanf:"conn_sleep"`
	// MaxSize is a human-readable cap ("25MB") rejected before streaming;
	// mirror clamd's StreamMaxLength. Empty or "0" means no client-side limit.
	MaxSize string `koanf:"max_size"`
	// FailOpen, when true, downgrades a failed boot-time ping to a WARN instead
	// of failing startup. SECURITY: default MUST be false (fail-closed).
	FailOpen bool `koanf:"fail_open"`
	// PingOnStart enables the boot-time reachability probe. Default true.
	PingOnStart bool `koanf:"ping_on_start"`
}

// ClamdOptions is the resolved, validated clamd backend config (MaxSize parsed
// to bytes). Constructed by loadOptions from Options.
type ClamdOptions struct {
	Network     string
	Address     string
	ConnTimeout time.Duration
	CmdTimeout  time.Duration
	ConnRetries int
	ConnSleep   time.Duration
	MaxSize     int64
}

func defaultOptions() Options {
	return Options{
		Backend:     backendClamd,
		Address:     "127.0.0.1:3310",
		Network:     "tcp",
		ConnTimeout: 5 * time.Second,
		CmdTimeout:  30 * time.Second,
		ConnRetries: 2,
		ConnSleep:   200 * time.Millisecond,
		MaxSize:     "25MB",
		FailOpen:    false, // SECURITY: fail-closed by default.
		PingOnStart: true,
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("storage.scan", &opts); err != nil {
		return Options{}, fmt.Errorf("storage/scan: load options: %w", err)
	}
	return opts, nil
}

// clamdOptionsFrom resolves Options into the validated ClamdOptions, parsing
// the human-readable size and defaulting the network.
func clamdOptionsFrom(opts Options) (ClamdOptions, error) {
	maxSize, err := parseSize(opts.MaxSize)
	if err != nil {
		return ClamdOptions{}, fmt.Errorf("storage/scan: resolve max_size: %w", err)
	}
	network := opts.Network
	if network == "" {
		network = "tcp"
	}
	return ClamdOptions{
		Network:     network,
		Address:     opts.Address,
		ConnTimeout: opts.ConnTimeout,
		CmdTimeout:  opts.CmdTimeout,
		ConnRetries: opts.ConnRetries,
		ConnSleep:   opts.ConnSleep,
		MaxSize:     maxSize,
	}, nil
}

// newScanner builds the configured Scanner and, for the clamd backend, wires a
// bounded boot-time ping via fx.Lifecycle. NO init() side effects.
func newScanner(
	opts Options,
	logger *slog.Logger,
	clk clock.Clock,
	lc fx.Lifecycle,
) (Scanner, error) {
	switch opts.Backend {
	case backendClamd, "":
		return newClamdBackend(opts, logger, clk, lc)
	case backendNoop:
		return newNoopScanner(logger), nil
	default:
		return nil, fmt.Errorf("storage/scan: unknown backend %q", opts.Backend)
	}
}

// newClamdBackend constructs the clamd scanner and registers its lifecycle hook.
func newClamdBackend(
	opts Options,
	logger *slog.Logger,
	clk clock.Clock,
	lc fx.Lifecycle,
) (Scanner, error) {
	copts, err := clamdOptionsFrom(opts)
	if err != nil {
		return nil, err
	}
	scanner, err := newClamdScanner(copts, logger, clk)
	if err != nil {
		return nil, err
	}
	logger.Debug(
		"storage/scan: started",
		slog.String("backend", backendClamd),
		slog.String("network", copts.Network),
		slog.String("address", copts.Address),
		slog.Int64("max_size", copts.MaxSize),
		slog.Bool("fail_open", opts.FailOpen),
	)
	if opts.PingOnStart {
		lc.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				return pingOnStart(ctx, scanner, opts.FailOpen, logger)
			},
			// OnStop is a no-op: dial-per-command means no pooled conns to drain.
		})
	}
	return scanner, nil
}

// pingOnStart runs a bounded boot-time reachability probe. With FailOpen it logs
// a WARN and continues; otherwise an unreachable daemon fails startup fast.
func pingOnStart(ctx context.Context, scanner Scanner, failOpen bool, logger *slog.Logger) error {
	pingCtx, cancel := context.WithTimeout(ctx, startPingTimeout)
	defer cancel()
	if err := scanner.Ping(pingCtx); err != nil {
		if failOpen {
			logger.WarnContext(ctx, "storage/scan: clamd unreachable at boot, continuing (fail_open)",
				slog.String("error", err.Error()))
			return nil
		}
		return fmt.Errorf("storage/scan: boot-time ping: %w", err)
	}
	logger.DebugContext(ctx, "storage/scan: clamd reachable")
	return nil
}

// Module provides scan.Scanner to the fx graph.
var Module = fx.Module(
	"golusoris.storage.scan",
	fx.Provide(loadOptions),
	fx.Provide(newScanner),
)
