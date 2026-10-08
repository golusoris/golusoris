<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — mcp/

Reusable **Model Context Protocol (MCP) server** fx module. Lets downstream
golusoris app expose its OWN tools over MCP (stdio or streamable-HTTP) without
hand-wiring transport lifecycle. Built on official MCP Go SDK
(`github.com/modelcontextprotocol/go-sdk`), re-exported so apps never import raw SDK.

This is in-app analogue of `cmd/golusoris-mcp` (framework's own
standalone MCP server). Apps wire `mcp.Module` and register tools via fx.

## Key surface

| Symbol | Purpose |
| --- | --- |
| `mcp.Module` | Provides a tool-less `*mcp.Server` and runs the configured transport under the fx lifecycle |
| `mcp.Server` (`= sdk.Server`) | App registers tools on this via `fx.Invoke` |
| `mcp.AddTool` | Typed registration; schema inference, validation, unmarshal, output handling |
| `mcp.Tool`, `mcp.CallToolRequest/Result`, `mcp.Content`, `mcp.TextContent` | SDK re-exports for tool registration |
| `mcp.ToolHandler` | Low-level escape hatch; no input or output validation |
| `mcp.Options` | `transport` (`stdio`\|`http`), `http.addr`, `http.path`, `name`, `version` |
| `mcp.TransportStdio` / `mcp.TransportHTTP` | Transport selectors |
| `mcp.EndStreamsOnShutdown` | Wraps streamable-HTTP handler; GET event streams end once `http.Server.Shutdown` begins, tool calls keep draining |
| `mcp.HTTPShutdownGrace` | Graceful-shutdown bound (10s); exceeds net/http's 5s wait on never-used connections (golang/go#22682) |

## Config keys (prefix `mcp`)

```text
mcp.transport     # "stdio" (default) or "http" (streamable-HTTP)
mcp.http.addr     # listen address for http transport (default ":8899")
mcp.http.path     # mount path for http transport (default "/mcp")
mcp.name          # server name advertised to clients (default "golusoris-mcp")
mcp.version       # server version advertised to clients (default "0.1.0")
```

## Wiring

```go
type pingInput struct{}

fx.New(
    golusoris.Core,
    mcp.Module,                                   // provides *mcp.Server
    fx.Invoke(func(s *mcp.Server) {               // app registers its tools
        mcp.AddTool[pingInput, any](
            s,
            &mcp.Tool{Name: "ping"},
            func(ctx context.Context, _ *mcp.CallToolRequest, _ pingInput) (*mcp.CallToolResult, any, error) {
                result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}
                return result, nil, nil
            },
        )
    }),
)
```

- **stdio** mode: `Server.Run` is launched on fx Start; on client disconnect
 Run returns and module ends app via `fx.Shutdowner` (CLI-style exit).
- **http** mode: dedicated `*http.Server` serves streamable-HTTP handler
 at `http.path`, gracefully shut down on fx Stop. Open GET event streams end
 when shutdown begins; in-flight tool calls drain within `HTTPShutdownGrace`.

## Stdout purity (stdio mode)

stdio transport owns **stdout** for JSON-RPC framing — single stray
stdout write corrupts protocol. module pins real stdout to transport and redirects process-global `os.Stdout` to **stderr** for transport's lifetime, so stray `fmt.Println` in app/library code lands on
stderr instead of breaking stream. Logs/otel/fx events already go to stderr
via `github.com/golusoris/golusoris/core/log`.

## Don't

- Don't write to `os.Stdout` directly in stdio mode — it's reserved for protocol. Module redirects strays to stderr; rely on `core/log`.
- Don't use low-level `Server.AddTool` unless the handler validates raw input
 and output itself. Prefer generic `mcp.AddTool`; invalid input never reaches handler.
- Don't import `github.com/modelcontextprotocol/go-sdk` directly — use this
 package's re-exports so transport/protocol negotiation stays correct.
- Don't add second HTTP listener for `http` mode if you already run
 `httpx/server`; mount SDK handler there instead if you need shared port,
 wrapped in `mcp.EndStreamsOnShutdown` so connected clients can't hold Stop.
