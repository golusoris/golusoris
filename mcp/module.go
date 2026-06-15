package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/fx"
)

// httpShutdownGrace bounds the streamable-HTTP server's graceful shutdown.
const httpShutdownGrace = 5 * time.Second

// runParams carries the dependencies the transport runner needs.
type runParams struct {
	fx.In

	Lifecycle  fx.Lifecycle
	Opts       Options
	Server     *Server
	Logger     *slog.Logger
	Shutdowner fx.Shutdowner
}

// run wires the configured transport into the fx lifecycle.
func run(p runParams) error {
	switch p.Opts.Transport {
	case TransportStdio:
		runStdio(p)
		return nil
	case TransportHTTP:
		runHTTP(p)
		return nil
	default:
		return fmt.Errorf("mcp: unknown transport %q", p.Opts.Transport)
	}
}

// runStdio launches the stdio transport on fx Start in its own goroutine. The
// transport writes JSON-RPC frames to the pinned real stdout while os.Stdout is
// redirected to stderr for the transport's lifetime (stdout purity). When Run
// returns (client disconnect or context cancel) the app is shut down via the
// fx.Shutdowner — matching a CLI MCP server launched by an IDE.
func runStdio(p runParams) {
	ctx, cancel := context.WithCancel(context.Background())
	var redirect *stdoutRedirect

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			r, realStdout, err := installStdoutRedirect(nil)
			if err != nil {
				cancel()
				return err
			}
			redirect = r
			// IOTransport over the pinned real stdout so framing bypasses the
			// redirected os.Stdout; stdin is an *os.File (io.ReadCloser).
			transport := newStdioTransport(stdin, realStdout)
			p.Logger.Info("mcp: serving on stdio")
			go func() {
				err := p.Server.Run(ctx, transport)
				if err != nil && !errors.Is(err, context.Canceled) {
					p.Logger.Error("mcp: stdio transport exited", slog.Any("err", err))
				}
				// Run returned: the client disconnected (or we were cancelled).
				// End the app so the process exits like a CLI MCP server.
				_ = p.Shutdowner.Shutdown()
			}()
			return nil
		},
		OnStop: func(context.Context) error {
			cancel()
			if redirect != nil {
				return redirect.Close()
			}
			return nil
		},
	})
}

// runHTTP serves the streamable-HTTP transport under its own *http.Server on
// fx Start and gracefully shuts it down on fx Stop. A serve error before
// shutdown triggers an app-wide shutdown.
func runHTTP(p runParams) {
	handler := sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *Server { return p.Server },
		nil,
	)
	mux := http.NewServeMux()
	mux.Handle(p.Opts.HTTP.Path, handler)
	srv := &http.Server{
		Addr:              p.Opts.HTTP.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second, // slow-loris guard
		// No WriteTimeout: streamable HTTP may stream responses indefinitely.
		IdleTimeout: 60 * time.Second,
	}

	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var lc net.ListenConfig
			ln, err := lc.Listen(ctx, "tcp", srv.Addr)
			if err != nil {
				return fmt.Errorf("mcp: listen %s: %w", srv.Addr, err)
			}
			p.Logger.InfoContext(
				ctx, "mcp: serving streamable-HTTP",
				slog.String("addr", ln.Addr().String()),
				slog.String("path", p.Opts.HTTP.Path),
			)
			go func() {
				if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					p.Logger.ErrorContext(ctx, "mcp: streamable-HTTP serve failed", slog.Any("err", serveErr))
					_ = p.Shutdowner.Shutdown()
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			shutCtx, cancel := context.WithTimeout(ctx, httpShutdownGrace)
			defer cancel()
			if err := srv.Shutdown(shutCtx); err != nil {
				return fmt.Errorf("mcp: shutdown: %w", err)
			}
			p.Logger.InfoContext(ctx, "mcp: streamable-HTTP shutdown complete")
			return nil
		},
	})
}

// Module provides a *mcp.Server (no tools) and runs the configured transport
// under the fx lifecycle. Apps register tools via fx.Invoke against the
// provided *Server. Config keys live under the "mcp" prefix.
var Module = fx.Module(
	"golusoris.mcp",
	fx.Provide(loadOptions),
	fx.Provide(newServer),
	fx.Invoke(run),
)
