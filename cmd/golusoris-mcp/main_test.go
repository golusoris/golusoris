package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// TestServerRoundTrip drives the real MCP protocol over an in-memory transport,
// proving the official-SDK server advertises the tools and dispatches calls.
func TestServerRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logger := slog.New(slog.DiscardHandler)

	serverT, clientT := mcp.NewInMemoryTransports()
	serverSession, err := newServer(logger).Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

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

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "golusoris_init",
		Arguments: map[string]any{"name": "blog"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("call tool returned IsError; content=%v", res.Content)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	if !strings.Contains(text.Text, "golusoris init blog") {
		t.Errorf("call result = %q, want substring %q", text.Text, "golusoris init blog")
	}
}
