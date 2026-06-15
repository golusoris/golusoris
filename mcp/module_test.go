package mcp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/mcp"
)

// discardLogger returns a stderr-safe no-op logger for tests.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// freeAddr reserves a loopback TCP port, closes the listener, and returns the
// address. There is a small reuse race, but it is the standard way to get a
// free port for an fx-managed server that binds the address itself.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close reserved listener: %v", err)
	}
	return addr
}

// cfgFromYAML builds a config.Config from an in-memory YAML body written to a
// temp file. Tests construct the "mcp" sub-tree.
func cfgFromYAML(t *testing.T, body string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.New(config.Options{Files: []string{path}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

// TestModule_HTTPTransport boots the Module with the http transport, has the
// app register a tool, then connects a real MCP client over streamable-HTTP
// and asserts the tool is listed and callable.
func TestModule_HTTPTransport(t *testing.T) {
	t.Parallel()
	addr := freeAddr(t)
	cfg := cfgFromYAML(t, "mcp:\n  transport: http\n  http:\n    addr: \""+addr+"\"\n")

	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(discardLogger),
		mcp.Module,
		fx.Invoke(func(s *mcp.Server) {
			s.AddTool(
				&mcp.Tool{
					Name:        "ping",
					Description: "returns pong",
					InputSchema: json.RawMessage(`{"type":"object"}`),
				},
				func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
				},
			)
		}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{Endpoint: "http://" + addr + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(ctx, &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "ping" {
		t.Fatalf("expected one tool 'ping', got %+v", tools.Tools)
	}

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if got := textOf(t, res); got != "pong" {
		t.Fatalf("expected 'pong', got %q", got)
	}
}

// TestModule_RejectsUnknownTransport asserts the module fails to start when the
// configured transport is invalid (caught at provide time by loadOptions).
func TestModule_RejectsUnknownTransport(t *testing.T) {
	t.Parallel()
	cfg := cfgFromYAML(t, "mcp:\n  transport: carrier-pigeon\n")

	app := fx.New(
		fx.NopLogger,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(discardLogger),
		mcp.Module,
	)
	if app.Err() == nil {
		t.Fatal("expected an error for an unknown transport, got nil")
	}
}

// TestModule_ServerHasNoToolsByDefault confirms the module ships a server with
// zero tools — apps register their own.
func TestModule_ServerHasNoToolsByDefault(t *testing.T) {
	t.Parallel()
	cfg := cfgFromYAML(t, "mcp:\n  transport: http\n  http:\n    addr: \""+freeAddr(t)+"\"\n")

	var srv *mcp.Server
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(discardLogger),
		mcp.Module,
		fx.Populate(&srv),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	if srv == nil {
		t.Fatal("expected a non-nil *mcp.Server")
	}
}

func textOf(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("empty tool result content")
	}
	tc, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("expected *TextContent, got %T", res.Content[0])
	}
	return tc.Text
}
