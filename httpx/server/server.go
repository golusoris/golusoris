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
//	http.limits.body         # max request body in bytes, 0 disables (default 10 MiB)
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

	"github.com/golusoris/golusoris/config"
)

// Options tunes the server. Durations accept koanf strings like "30s".
type Options struct {
	Addr     string         `koanf:"addr"`
	Timeouts TimeoutOptions `koanf:"timeouts"`
	Limits   LimitOptions   `koanf:"limits"`
}

// TimeoutOptions groups the server's timeouts.
type TimeoutOptions struct {
	Read     time.Duration `koanf:"read"`
	Header   time.Duration `koanf:"header"` // read-header timeout (slow-loris guard)
	Write    time.Duration `koanf:"write"`
	Idle     time.Duration `koanf:"idle"`
	Shutdown time.Duration `koanf:"shutdown"`
}

// LimitOptions groups the server's size limits. Body == 0 disables body cap.
type LimitOptions struct {
	Header int   `koanf:"header"` // max header size in bytes
	Body   int64 `koanf:"body"`   // max request body in bytes
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
	if o.Timeouts.Read == 0 {
		o.Timeouts.Read = d.Timeouts.Read
	}
	if o.Timeouts.Header == 0 {
		o.Timeouts.Header = d.Timeouts.Header
	}
	if o.Timeouts.Write == 0 {
		o.Timeouts.Write = d.Timeouts.Write
	}
	if o.Timeouts.Idle == 0 {
		o.Timeouts.Idle = d.Timeouts.Idle
	}
	if o.Timeouts.Shutdown == 0 {
		o.Timeouts.Shutdown = d.Timeouts.Shutdown
	}
	if o.Limits.Header == 0 {
		o.Limits.Header = d.Limits.Header
	}
	// Limits.Body == 0 means "disabled" — don't override.
	return o
}

// New builds a *http.Server wrapping handler with the configured timeouts +
// body-size enforcement.
func New(handler http.Handler, opts Options) *http.Server {
	opts = opts.withDefaults()
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

// bodyLimitMiddleware caps request body size. Requests that exceed the limit
// surface as io.ErrUnexpectedEOF to the handler on Read; apps should handle
// that as a 413.
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
// httpx/autotls/* sub-modules.
type serverParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	Opts      Options
	Handler   http.Handler
	Logger    *slog.Logger
	TLSConfig *tls.Config `optional:"true"`
}

// Module provides a *http.Server that listens during fx Start and is
// gracefully shut down during fx Stop. Requires a [http.Handler] in the
// graph (see [httpx/router.Module]). If a *tls.Config is provided
// (optionally, via one of the httpx/autotls sub-modules), the server
// listens over TLS.
var Module = fx.Module("golusoris.httpx.server",
	fx.Provide(loadOptions),
	fx.Provide(func(p serverParams) *http.Server {
		srv := New(p.Handler, p.Opts)
		srv.TLSConfig = p.TLSConfig

		p.Lifecycle.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				rawLn, err := net.Listen("tcp", srv.Addr)
				if err != nil {
					return fmt.Errorf("httpx/server: listen %s: %w", srv.Addr, err)
				}
				ln := rawLn
				scheme := "http"
				if p.TLSConfig != nil {
					ln = tls.NewListener(rawLn, p.TLSConfig)
					scheme = "https"
				}
				p.Logger.Info("httpx/server: listening",
					slog.String("addr", ln.Addr().String()),
					slog.String("scheme", scheme),
				)
				go func() {
					if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
						p.Logger.Error("httpx/server: serve failed", slog.String("error", serveErr.Error()))
					}
				}()
				_ = ctx // Start ctx is advisory — we don't block on it.
				return nil
			},
			OnStop: func(ctx context.Context) error {
				shutdownCtx, cancel := context.WithTimeout(ctx, p.Opts.Timeouts.Shutdown)
				defer cancel()
				if err := srv.Shutdown(shutdownCtx); err != nil {
					return fmt.Errorf("httpx/server: shutdown: %w", err)
				}
				p.Logger.Info("httpx/server: shutdown complete")
				return nil
			},
		})
		return srv
	}),
)
