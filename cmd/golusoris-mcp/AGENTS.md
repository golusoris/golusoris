<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — cmd/golusoris-mcp/

Standalone MCP server that exposes golusoris scaffolding as MCP tools.
Built on official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`).

## Transports

- **stdio** (default) — for local IDE clients (Claude Desktop, Cursor) that
 launch binary directly.
- **streamable-HTTP** — `--transport http` serves `/mcp` on `--addr` (`:8899`).

```sh
golusoris-mcp                    # stdio
golusoris-mcp --transport http   # streamable-HTTP on :8899
```

## Tools exposed

| Tool | Description |
|---|---|
| `golusoris_init` | Scaffold new app |
| `golusoris_add` | Show how to add module |
| `golusoris_bump` | Show how to bump golusoris version |

Closed object schemas. SDK generic registration validates required fields,
types, and unknown fields before dispatch.

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
 as instructions to agent, not side effects.
- Don't re-introduce hand-rolled JSON-RPC. Register tools via generic
 `mcp.AddTool`. Low-level `server.AddTool` skips input validation.
