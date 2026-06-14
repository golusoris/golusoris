# Agent guide — cmd/golusoris-mcp/

Standalone MCP server that exposes golusoris scaffolding as MCP tools.
Built on the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`).

## Transports

- **stdio** (default) — for local IDE clients (Claude Desktop, Cursor) that
  launch the binary directly.
- **streamable-HTTP** — `--transport http` serves `/mcp` on `--addr` (`:8899`).

```sh
golusoris-mcp                    # stdio
golusoris-mcp --transport http   # streamable-HTTP on :8899
```

## Tools exposed

| Tool | Description |
|---|---|
| `golusoris_init` | Scaffold a new app |
| `golusoris_add` | Show how to add a module |
| `golusoris_bump` | Show how to bump golusoris version |

Tool schemas are kept wire-identical to the pre-SDK implementation.

## MCP client config (Claude Desktop / Cursor — stdio)

```json
{
  "mcpServers": {
    "golusoris": {
      "command": "golusoris-mcp"
    }
  }
}
```

## Don't

- Don't add tools that shell out to arbitrary commands — keep tool output
  as instructions to the agent, not side effects.
- Don't re-introduce hand-rolled JSON-RPC; register tools via the SDK
  (`server.AddTool`) so transport/protocol negotiation stays correct.
