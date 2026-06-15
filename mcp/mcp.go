// Package mcp wires a reusable Model Context Protocol (MCP) server into the fx
// lifecycle. It lets a downstream golusoris app expose its OWN tools over MCP
// without hand-rolling the transport boilerplate.
//
// The module provides a [*Server] (the official SDK's server) with no tools of
// its own; the app registers tools after the fact:
//
//	fx.New(
//	    golusoris.Core,
//	    mcp.Module,                                  // provides *mcp.Server
//	    fx.Invoke(func(s *mcp.Server) {              // app registers its tools
//	        s.AddTool(&mcp.Tool{Name: "ping"}, pingHandler)
//	    }),
//	)
//
// The module reads its transport from config and runs it under an fx lifecycle
// hook:
//
//	mcp.transport     # "stdio" (default) or "http" (streamable-HTTP)
//	mcp.http.addr     # listen address for the http transport (default ":8899")
//	mcp.http.path     # mount path for the http transport (default "/mcp")
//	mcp.name          # server name advertised to clients (default app binary)
//	mcp.version       # server version advertised to clients (default "0.1.0")
//
// In stdio mode the transport owns stdout for JSON-RPC framing: a single stray
// stdout write corrupts the protocol. The module therefore pins the real
// stdout to the transport and redirects the process-global os.Stdout to stderr
// for the lifetime of the transport (see [redirectStdout]) — so a stray
// fmt.Println in app or library code lands on stderr instead of corrupting the
// protocol. The golusoris log module already defaults its handler to stderr.
//
// On stdio client disconnect the transport's Run returns, which the module
// turns into an app-wide shutdown via [fx.Shutdowner] — matching the behaviour
// of a CLI MCP server launched by Claude Desktop / Cursor.
package mcp

import (
	"fmt"
	"log/slog"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/golusoris/golusoris/config"
)

// Re-exports of the SDK types an app needs to register tools, so callers import
// only this package (and never the raw SDK).
type (
	// Server is the MCP server an app registers tools on.
	Server = sdkmcp.Server
	// Tool describes a single MCP tool.
	Tool = sdkmcp.Tool
	// ToolHandler handles a tools/call invocation.
	ToolHandler = sdkmcp.ToolHandler
	// CallToolRequest is the inbound tools/call request.
	CallToolRequest = sdkmcp.CallToolRequest
	// CallToolResult is the tools/call result.
	CallToolResult = sdkmcp.CallToolResult
	// Content is a single content block in a tool result.
	Content = sdkmcp.Content
	// TextContent is a text content block.
	TextContent = sdkmcp.TextContent
)

// Transport selects the MCP transport.
type Transport string

// Transport values.
const (
	// TransportStdio runs JSON-RPC over stdin/stdout — for local IDE clients
	// that launch the binary directly (Claude Desktop, Cursor).
	TransportStdio Transport = "stdio"
	// TransportHTTP serves the streamable-HTTP transport on an address.
	TransportHTTP Transport = "http"
)

// Defaults for the http transport and server identity.
const (
	defaultAddr    = ":8899"
	defaultPath    = "/mcp"
	defaultName    = "golusoris-mcp"
	defaultVersion = "0.1.0"
)

// Options tunes the MCP transport. Config keys live under the "mcp" prefix.
type Options struct {
	// Transport selects "stdio" (default) or "http".
	Transport Transport `koanf:"transport"`
	// HTTP configures the streamable-HTTP transport (used when transport=http).
	HTTP HTTPOptions `koanf:"http"`
	// Name is the server name advertised to clients.
	Name string `koanf:"name"`
	// Version is the server version advertised to clients.
	Version string `koanf:"version"`
}

// HTTPOptions configures the streamable-HTTP transport.
type HTTPOptions struct {
	// Addr is the listen address (default ":8899").
	Addr string `koanf:"addr"`
	// Path is the mount path for the MCP handler (default "/mcp").
	Path string `koanf:"path"`
}

// DefaultOptions returns the opinionated defaults.
func DefaultOptions() Options {
	return Options{
		Transport: TransportStdio,
		HTTP:      HTTPOptions{Addr: defaultAddr, Path: defaultPath},
		Name:      defaultName,
		Version:   defaultVersion,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.Transport == "" {
		o.Transport = d.Transport
	}
	if o.HTTP.Addr == "" {
		o.HTTP.Addr = d.HTTP.Addr
	}
	if o.HTTP.Path == "" {
		o.HTTP.Path = d.HTTP.Path
	}
	if o.Name == "" {
		o.Name = d.Name
	}
	if o.Version == "" {
		o.Version = d.Version
	}
	return o
}

// validate rejects unknown transports up front so misconfiguration fails fast
// at provide time rather than mid-startup.
func (o Options) validate() error {
	switch o.Transport {
	case TransportStdio, TransportHTTP:
		return nil
	default:
		return fmt.Errorf("mcp: unknown transport %q (want %q or %q)", o.Transport, TransportStdio, TransportHTTP)
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("mcp", &opts); err != nil {
		return Options{}, fmt.Errorf("mcp: load options: %w", err)
	}
	opts = opts.withDefaults()
	if err := opts.validate(); err != nil {
		return Options{}, err
	}
	return opts, nil
}

// newServer builds the MCP server with no tools registered. The injected
// logger is wired into the SDK so server activity logs land on the app's
// stderr-bound handler. Apps register tools via fx.Invoke against the
// returned *Server.
func newServer(opts Options, logger *slog.Logger) *Server {
	return sdkmcp.NewServer(
		&sdkmcp.Implementation{Name: opts.Name, Version: opts.Version},
		&sdkmcp.ServerOptions{Logger: logger},
	)
}
