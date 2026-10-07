// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package server wires [*http.Server] as an fx dependency with slow-loris
// guards, body-size limits, and graceful shutdown.
//
// Apps mount routes by injecting the [http.Handler] dependency (provided by
// [router.Module]) and decorating it before handing to this module, or
// simply by wiring [router.Module] alongside [Module] so the Server picks up
// the router's Handler automatically.
//
// Config keys (env prefix APP_):
//
//	http.addr                # listen address (default ":8080")
//	http.timeouts.read       # total read deadline (default 30s)
//	http.timeouts.header     # header deadline — slow-loris guard (default 5s)
//	http.timeouts.write      # write deadline (default 60s)
//	http.timeouts.idle       # keep-alive idle deadline (default 120s)
//	http.timeouts.shutdown   # graceful-shutdown grace (default 30s)
//	http.limits.header       # max header size in bytes (default 1 MiB)
//	http.limits.body         # max request body in bytes (default 10 MiB)
//	http.limits.unlimited    # explicitly disable the request-body cap
//	http.tls.cert            # PEM certificate chain; enables HTTPS (TLS 1.3, reloaded on rotation)
//	http.tls.key             # PEM private key
//	http.tls.ca              # client-CA bundle for mTLS
//	http.tls.clientauth      # none|request|require_any|verify_if_given|require_and_verify
//	                         # (default: require_and_verify with ca, else none)
//
// File-based TLS and an autotls provider are mutually exclusive; wiring both
// fails at construction.
//
// Fields use single-word koanf keys grouped under sub-structs because the
// default env→koanf transform ("_" → path separator) can't distinguish
// path separators from word separators.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/drain"
	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/validate"
)

var (
	errTLSConflict = errors.New("httpx/server: http.tls.* conflicts with an autotls *tls.Config")
	errTLSPair     = errors.New("httpx/server: http.tls needs both cert and key")
)

// Options tunes the server. Durations accept koanf strings like "30s".
type Options struct {
	Addr     string         `koanf:"addr"`
	Timeouts TimeoutOptions `koanf:"timeouts"`
	Limits   LimitOptions   `koanf:"limits"`
	TLS      TLSOptions     `koanf:"tls"`
}

// TimeoutOptions groups the server's timeouts.
type TimeoutOptions struct {
	Read     time.Duration `koanf:"read"`
	Header   time.Duration `koanf:"header"` // read-header timeout (slow-loris guard)
	Write    time.Duration `koanf:"write"`
	Idle     time.Duration `koanf:"idle"`
	Shutdown time.Duration `koanf:"shutdown"`
}

// LimitOptions groups the server's size limits.
type LimitOptions struct {
	Header             int   `koanf:"header"`    // max header size in bytes
	Body               int64 `koanf:"body"`      // max request body in bytes
	AllowUnlimitedBody bool  `koanf:"unlimited"` // explicitly disable body limit
}

// TLSOptions serves HTTPS from PEM files that reload on handshake. The zero
// value leaves TLS to an optional autotls provider.
type TLSOptions struct {
	Cert       string `koanf:"cert"`       // certificate chain path
	Key        string `koanf:"key"`        // private key path
	CA         string `koanf:"ca"`         // client-CA bundle that verifies client certificates
	ClientAuth string `koanf:"clientauth"` // client-certificate policy (core/tlsx ParseClientAuth modes)
}

// DefaultOptions returns the opinionated defaults.
func DefaultOptions() Options {
	return Options{
		Addr: ":8080",
		Timeouts: TimeoutOptions{
			Read:     30 * time.Second,
			Header:   5 * time.Second,
			Write:    60 * time.Second,
			Idle:     120 * time.Second,
			Shutdown: 30 * time.Second,
		},
		Limits: LimitOptions{
			Header: 1 << 20,  // 1 MiB
			Body:   10 << 20, // 10 MiB
		},
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.Addr == "" {
		o.Addr = d.Addr
	}
	if o.Timeouts.Read <= 0 {
		o.Timeouts.Read = d.Timeouts.Read
	}
	if o.Timeouts.Header <= 0 {
		o.Timeouts.Header = d.Timeouts.Header
	}
	if o.Timeouts.Write <= 0 {
		o.Timeouts.Write = d.Timeouts.Write
	}
	if o.Timeouts.Idle <= 0 {
		o.Timeouts.Idle = d.Timeouts.Idle
	}
	if o.Timeouts.Shutdown <= 0 {
		o.Timeouts.Shutdown = d.Timeouts.Shutdown
	}
	if o.Limits.Header <= 0 {
		o.Limits.Header = d.Limits.Header
	}
	if o.Limits.AllowUnlimitedBody {
		o.Limits.Body = 0
	} else if o.Limits.Body <= 0 {
		o.Limits.Body = d.Limits.Body
	}
	return o
}

// New builds a *http.Server wrapping handler with the configured timeouts +
// body-size enforcement.
func New(handler http.Handler, opts Options) *http.Server {
	opts = opts.withDefaults()
	if validate.IsNil(handler) {
		handler = http.NotFoundHandler()
	}
	if opts.Limits.Body > 0 {
		handler = bodyLimitMiddleware(handler, opts.Limits.Body)
	}
	return &http.Server{
		Addr:              opts.Addr,
		Handler:           handler,
		ReadTimeout:       opts.Timeouts.Read,
		ReadHeaderTimeout: opts.Timeouts.Header,
		WriteTimeout:      opts.Timeouts.Write,
		IdleTimeout:       opts.Timeouts.Idle,
		MaxHeaderBytes:    opts.Limits.Header,
	}
}

// bodyLimitMiddleware caps request body size. Reads beyond the limit return
// *http.MaxBytesError; apps should translate that error to HTTP 413.
func bodyLimitMiddleware(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("http", &opts); err != nil {
		return Options{}, fmt.Errorf("httpx/server: load options: %w", err)
	}
	return opts.withDefaults(), nil
}

// serverParams lets fx supply an optional *tls.Config. If present, the
// listener is wrapped with TLS. Apps wire this by including one of the
// httpx/autotls/* sub-modules, or set http.tls.* instead.
type serverParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	Opts      Options
	Handler   http.Handler
	Logger    *slog.Logger
	TLSConfig *tls.Config `optional:"true"`
	// Gate, when k8s/health.Module is wired, holds Shutdown until readiness has drained.
	Gate drain.Gate `optional:"true"`
	// Clock paces TLS file reloads; the wall clock when absent.
	Clock clock.Clock `optional:"true"`
}

// resolveTLS returns the autotls config, a reloading config built from
// http.tls.*, or nil for plaintext.
func resolveTLS(opts TLSOptions, provided *tls.Config, logger *slog.Logger, clk clock.Clock) (*tls.Config, error) {
	if opts == (TLSOptions{}) {
		return provided, nil
	}
	if provided != nil {
		return nil, errTLSConflict
	}
	if opts.Cert == "" || opts.Key == "" {
		return nil, errTLSPair
	}
	auth, err := tlsx.ParseClientAuth(opts.ClientAuth, opts.CA != "")
	if err != nil {
		return nil, fmt.Errorf("httpx/server: tls clientauth: %w", err)
	}
	reloader, err := tlsx.NewReloader(tlsx.Files{Cert: opts.Cert, Key: opts.Key, CA: opts.CA}, tlsx.Options{Clock: clk, Logger: logger})
	if err != nil {
		return nil, fmt.Errorf("httpx/server: load tls files: %w", err)
	}
	cfg := reloader.ServerConfig(auth)
	// "h2" here makes http.Server.Serve enable HTTP/2 on the TLS listener.
	cfg.NextProtos = []string{"h2", "http/1.1"}
	return cfg, nil
}

// Module provides a *http.Server that listens during fx Start and is
// gracefully shut down during fx Stop. Requires a [http.Handler] in the
// graph (see [httpx/router.Module]). If a *tls.Config is provided
// (optionally, via one of the httpx/autotls sub-modules) or http.tls.cert
// is set, the server listens over TLS. With k8s/health.Module wired, shutdown
// starts only after the readiness drain window, while the server keeps
// answering /readyz with 503.
var Module = fx.Module(
	"golusoris.httpx.server",
	fx.Provide(loadOptions),
	fx.Provide(func(p serverParams) (*http.Server, error) {
		tlsConfig, err := resolveTLS(p.Opts.TLS, p.TLSConfig, p.Logger, p.Clock)
		if err != nil {
			return nil, err
		}
		srv := New(p.Handler, p.Opts)
		srv.TLSConfig = tlsConfig
		p.Lifecycle.Append(drain.Wrap(p.Gate, serverHook(srv, p)))
		return srv, nil
	}),
)

// serverHook binds listen/serve and the bounded shutdown to the fx lifecycle.
func serverHook(srv *http.Server, p serverParams) fx.Hook {
	return fx.Hook{
		OnStart: func(ctx context.Context) error {
			rawLn, err := (&net.ListenConfig{}).Listen(ctx, "tcp", srv.Addr)
			if err != nil {
				return fmt.Errorf("httpx/server: listen %s: %w", srv.Addr, err)
			}
			ln := rawLn
			scheme := "http"
			if srv.TLSConfig != nil {
				ln = tls.NewListener(rawLn, srv.TLSConfig)
				scheme = "https"
			}
			p.Logger.InfoContext(
				ctx, "httpx/server: listening",
				slog.String("addr", ln.Addr().String()),
				slog.String("scheme", scheme),
			)
			go func() {
				if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					p.Logger.ErrorContext(ctx, "httpx/server: serve failed", slog.String("error", serveErr.Error()))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			shutdownCtx, cancel := context.WithTimeout(ctx, p.Opts.Timeouts.Shutdown)
			defer cancel()
			if err := srv.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("httpx/server: shutdown: %w", err)
			}
			p.Logger.InfoContext(ctx, "httpx/server: shutdown complete")
			return nil
		},
	}
}
