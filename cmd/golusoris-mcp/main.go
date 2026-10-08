// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Command golusoris-mcp is a standalone MCP server that exposes golusoris
// framework capabilities as MCP tools. It allows AI agents (Claude, Cursor,
// Copilot, etc.) to scaffold apps, add modules, and query framework docs.
//
// Usage:
//
//	golusoris-mcp                       # stdio transport (for local IDE clients)
//	golusoris-mcp --transport http      # streamable-HTTP on :8899
//	golusoris-mcp --transport http --addr :9000
//
// The server is built on the official MCP Go SDK
// (github.com/modelcontextprotocol/go-sdk). The stdio transport lets Claude
// Desktop / Cursor launch the binary directly; the http transport serves the
// streamable-HTTP transport at /mcp for remote clients.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/core/clikit"
	"github.com/golusoris/golusoris/core/mcp"
)

const (
	serverName    = "golusoris-mcp"
	serverVersion = "0.1.0"
)

func main() {
	var (
		addr      string
		transport string
	)

	root := clikit.New(serverName, "MCP server for the golusoris framework")
	flags := root.Cobra().PersistentFlags()
	flags.StringVar(&transport, "transport", "stdio", "Transport: 'stdio' (local IDE clients) or 'http' (streamable-HTTP)")
	flags.StringVar(&addr, "addr", ":8899", "Listen address (http transport only)")
	root.Cobra().RunE = func(cmd *cobra.Command, _ []string) error {
		return run(cmd.Context(), transport, addr)
	}
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, transport, addr string) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	switch transport {
	case "stdio":
		return runStdio(ctx, logger)
	case "http":
		return runHTTP(ctx, logger, addr)
	default:
		return fmt.Errorf("unknown transport %q (want 'stdio' or 'http')", transport)
	}
}

// newServer builds the MCP server with all golusoris tools registered.
func newServer(logger *slog.Logger) *sdkmcp.Server {
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: serverName, Version: serverVersion}, &sdkmcp.ServerOptions{Logger: logger})
	for i := range mcpTools {
		t := mcpTools[i]
		sdkmcp.AddTool[map[string]any, any](
			srv,
			&sdkmcp.Tool{Name: t.name, Description: t.description, InputSchema: t.inputSchema},
			toolHandler(t.name),
		)
	}
	return srv
}

func runStdio(ctx context.Context, logger *slog.Logger) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger.InfoContext(ctx, "golusoris-mcp serving on stdio")
	if err := newServer(logger).Run(ctx, &sdkmcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("stdio transport: %w", err)
	}
	return nil
}

func runHTTP(ctx context.Context, logger *slog.Logger, addr string) error {
	handler := sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return newServer(logger) },
		nil,
	)
	srv := &http.Server{
		Addr:         addr,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // streamable HTTP may stream responses; no write deadline
		IdleTimeout:  60 * time.Second,
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.EndStreamsOnShutdown(ctx, srv, handler))
	srv.Handler = mux

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		logger.InfoContext(ctx, "golusoris-mcp listening", "addr", addr, "path", "/mcp")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.ErrorContext(ctx, "server error", "err", err)
		}
	}()

	<-ctx.Done()
	shutCtx, shutCancel := context.WithTimeout(context.WithoutCancel(ctx), mcp.HTTPShutdownGrace)
	defer shutCancel()
	return srv.Shutdown(shutCtx) //nolint:wrapcheck // stdlib error is descriptive
}

// mcpTool is a static tool definition exposed by the server.
type mcpTool struct {
	name        string
	description string
	inputSchema json.RawMessage
}

// mcpTools is the static catalog exposed by the MCP server. Schemas are kept
// wire-identical to the pre-SDK implementation so existing clients see no diff.
var mcpTools = []mcpTool{
	{
		name:        "golusoris_init",
		description: "Scaffold a new golusoris application with a go.mod and main.go.",
		inputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["name"],
			"additionalProperties": false,
			"properties": {
				"name": {"type": "string", "description": "App name (directory name)"},
				"module": {"type": "string", "description": "Go module path (default: github.com/example/<name>)"}
			}
		}`),
	},
	{
		name:        "golusoris_add",
		description: "Show how to add a golusoris module (db, http, otel, cache, jobs, auth-oidc, authz, k8s) to an existing app.",
		inputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["module"],
			"additionalProperties": false,
			"properties": {
				"module": {"type": "string", "description": "Module short name (e.g. 'db', 'http', 'jobs')"}
			}
		}`),
	},
	{
		name:        "golusoris_bump",
		description: "Show how to bump golusoris to a specific version in a downstream app.",
		inputSchema: json.RawMessage(`{
			"type": "object",
			"additionalProperties": false,
			"properties": {
				"version": {"type": "string", "description": "Target version (e.g. 'v0.5.0' or 'latest')"}
			}
		}`),
	},
}

// toolHandler dispatches schema-validated arguments to the corresponding
// golusoris CLI guidance.
func toolHandler(name string) sdkmcp.ToolHandlerFor[map[string]any, any] {
	return func(_ context.Context, _ *sdkmcp.CallToolRequest, args map[string]any) (*sdkmcp.CallToolResult, any, error) {
		return textResult(dispatchTool(name, args)), nil, nil
	}
}

func dispatchTool(name string, args map[string]any) string {
	switch name {
	case "golusoris_init":
		appName, _ := args["name"].(string)
		mod, _ := args["module"].(string)
		if mod == "" {
			mod = "github.com/example/" + appName
		}
		return fmt.Sprintf("Run:\n  golusoris init %s --module %s", appName, mod)

	case "golusoris_add":
		module, _ := args["module"].(string)
		return "Run:\n  golusoris add " + module

	case "golusoris_bump":
		version, _ := args["version"].(string)
		if version == "" {
			version = "latest"
		}
		if version != "latest" && !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		return fmt.Sprintf("Run:\n  golusoris bump %s\n\nCheck docs/migrations/ for breaking-change notes.", version)

	default:
		return "unknown tool: " + name
	}
}

func textResult(text string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}}}
}
