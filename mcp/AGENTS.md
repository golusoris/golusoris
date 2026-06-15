# Agent guide — mcp/

Reusable **Model Context Protocol (MCP) server** fx module. Lets a downstream
golusoris app expose its OWN tools over MCP (stdio or streamable-HTTP) without
hand-wiring the transport lifecycle. Built on the official MCP Go SDK
(`github.com/modelcontextprotocol/go-sdk`), re-exported so apps never import the
raw SDK.

This is the in-app analogue of `cmd/golusoris-mcp` (the framework's own
standalone MCP server). Apps wire `mcp.Module` and register tools via fx.

## Key surface

| Symbol | Purpose |
|---|---|
| `mcp.Module` | Provides a tool-less `*mcp.Server` and runs the configured transport under the fx lifecycle |
| `mcp.Server` (`= sdk.Server`) | App registers tools on this via `fx.Invoke` |
| `mcp.Tool`, `mcp.ToolHandler`, `mcp.CallToolRequest/Result`, `mcp.Content`, `mcp.TextContent` | SDK re-exports for tool registration |
| `mcp.Options` | `transport` (`stdio`\|`http`), `http.addr`, `http.path`, `name`, `version` |
| `mcp.TransportStdio` / `mcp.TransportHTTP` | Transport selectors |

## Config keys (prefix `mcp`)

```
mcp.transport     # "stdio" (default) or "http" (streamable-HTTP)
mcp.http.addr     # listen address for http transport (default ":8899")
mcp.http.path     # mount path for http transport (default "/mcp")
mcp.name          # server name advertised to clients (default "golusoris-mcp")
mcp.version       # server version advertised to clients (default "0.1.0")
```

## Wiring

```go
fx.New(
    golusoris.Core,
    mcp.Module,                                   // provides *mcp.Server
    fx.Invoke(func(s *mcp.Server) {               // app registers its tools
        s.AddTool(
            &mcp.Tool{Name: "ping", InputSchema: json.RawMessage(`{"type":"object"}`)},
            func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
                return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
            },
        )
    }),
)
```

- **stdio** mode: `Server.Run` is launched on fx Start; on client disconnect
  Run returns and the module ends the app via `fx.Shutdowner` (CLI-style exit).
- **http** mode: a dedicated `*http.Server` serves the streamable-HTTP handler
  at `http.path`, gracefully shut down on fx Stop.

## Stdout purity (stdio mode)

The stdio transport owns **stdout** for JSON-RPC framing — a single stray
stdout write corrupts the protocol. The module pins the real stdout to the
transport and redirects the process-global `os.Stdout` to **stderr** for the
transport's lifetime, so a stray `fmt.Println` in app/library code lands on
stderr instead of breaking the stream. Logs/otel/fx events already go to stderr
via `golusoris/log`.

## Don't

- Don't write to `os.Stdout` directly in stdio mode — it's reserved for the
  protocol (the module redirects strays to stderr, but rely on `golusoris/log`).
- Don't register tools without an `InputSchema` of `type: "object"` — the SDK
  panics on a missing schema (fail-fast by design).
- Don't import `github.com/modelcontextprotocol/go-sdk` directly — use this
  package's re-exports so transport/protocol negotiation stays correct.
- Don't add a second HTTP listener for `http` mode if you already run
  `httpx/server`; mount the SDK handler there instead if you need a shared port.
