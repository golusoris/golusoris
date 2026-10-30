// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDispatchTool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"init with module", "golusoris_init", map[string]any{"name": "blog", "module": "github.com/me/blog"}, "golusoris init blog --module github.com/me/blog"},
		{"init default module", "golusoris_init", map[string]any{"name": "blog"}, "github.com/example/blog"},
		{"add", "golusoris_add", map[string]any{"module": "db"}, "golusoris add db"},
		{"bump bare version gets v", "golusoris_bump", map[string]any{"version": "1.2.3"}, "golusoris bump v1.2.3"},
		{"bump default latest", "golusoris_bump", map[string]any{}, "golusoris bump latest"},
		{"unknown tool", "nope", nil, "unknown tool: nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := dispatchTool(tt.tool, tt.args); !strings.Contains(got, tt.want) {
				t.Errorf("dispatchTool(%q) = %q, want substring %q", tt.tool, got, tt.want)
			}
		})
	}
}

var invalidInitToolArgs = []struct {
	name string
	args map[string]any
}{
	{name: "missing required", args: map[string]any{}},
	{name: "wrong type", args: map[string]any{"name": 7}},
	{name: "extra property", args: map[string]any{"name": "blog", "unexpected": true}},
}

// connectInMemoryMCP sets up in-memory MCP server and client sessions.
func connectInMemoryMCP(t *testing.T, ctx context.Context) *sdkmcp.ClientSession {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	serverT, clientT := sdkmcp.NewInMemoryTransports()
	serverSession, err := newServer(logger).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// assertMCPToolsListed verifies all expected tools are returned by tools/list.
func assertMCPToolsListed(t *testing.T, ctx context.Context, cs *sdkmcp.ClientSession) {
	t.Helper()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != len(mcpTools) {
		t.Fatalf("tools/list = %d tools, want %d", len(tools.Tools), len(mcpTools))
	}
	got := map[string]bool{}
	for _, tl := range tools.Tools {
		got[tl.Name] = true
	}
	for _, want := range []string{"golusoris_init", "golusoris_add", "golusoris_bump"} {
		if !got[want] {
			t.Errorf("tools/list missing %q", want)
		}
	}
}

// assertValidInitToolCall verifies executing a valid golusoris_init tool call.
func assertValidInitToolCall(t *testing.T, ctx context.Context, cs *sdkmcp.ClientSession) {
	t.Helper()
	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "golusoris_init",
		Arguments: map[string]any{"name": "blog"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("call tool returned IsError; content=%v", res.Content)
	}
	text, ok := res.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *sdkmcp.TextContent", res.Content[0])
	}
	if !strings.Contains(text.Text, "golusoris init blog") {
		t.Errorf("call result = %q, want substring %q", text.Text, "golusoris init blog")
	}
}

// assertInvalidInitToolCalls verifies schema rejection on invalid arguments.
func assertInvalidInitToolCalls(t *testing.T, ctx context.Context, cs *sdkmcp.ClientSession) {
	t.Helper()
	for _, tt := range invalidInitToolArgs {
		got, callErr := cs.CallTool(ctx, &sdkmcp.CallToolParams{
			Name:      "golusoris_init",
			Arguments: tt.args,
		})
		if callErr != nil {
			t.Fatalf("%s: call schema-invalid tool: %v", tt.name, callErr)
		}
		if !got.IsError {
			t.Fatalf("%s: schema-invalid call returned success; content=%v", tt.name, got.Content)
		}
	}
}

// TestServerRoundTrip drives the real MCP protocol over an in-memory transport,
// proving the official-SDK server advertises the tools and dispatches calls.
func TestServerRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := connectInMemoryMCP(t, ctx)
	assertMCPToolsListed(t, ctx, cs)
	assertValidInitToolCall(t, ctx, cs)
	assertInvalidInitToolCalls(t, ctx, cs)
}

// newTestClient uses a private transport: httptest.Server.Close resets http.DefaultTransport (#701).
func newTestClient(t *testing.T) *http.Client {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

// TestRunHTTPStopsWithOpenSession asserts that a client still holding its
// event stream does not hold up the HTTP server's graceful shutdown.
func TestRunHTTPStopsWithOpenSession(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serveCtx, stop := context.WithCancel(ctx)
	served := make(chan error, 1)
	go func() { served <- serveHTTP(serveCtx, slog.New(slog.DiscardHandler), ln) }()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0"}, nil)
	transport := &sdkmcp.StreamableClientTransport{Endpoint: "http://" + ln.Addr().String() + "/mcp", HTTPClient: newTestClient(t)}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		stop()
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	stop()
	// Well below the shutdown grace: only ending the stream lets runHTTP return in time.
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("runHTTP with an open session: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runHTTP did not return within 3s of the stop signal")
	}
}

func TestRunHTTP_AddressAlreadyInUse(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = runHTTP(ctx, slog.New(slog.DiscardHandler), ln.Addr().String())
	if err == nil {
		t.Fatal("runHTTP on occupied address succeeded, want error")
	}
	if !strings.Contains(err.Error(), "golusoris-mcp: listen:") {
		t.Fatalf("runHTTP error = %v, want golusoris-mcp: listen: prefix", err)
	}
}
