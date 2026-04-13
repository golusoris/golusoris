# Agent guide — cmd/golusoris-mcp/

Standalone MCP server that exposes golusoris scaffolding as MCP tools.
Implements JSON-RPC 2.0 MCP protocol at `POST /mcp`.

## Running

```sh
golusoris-mcp --addr :8899
```

## Tools exposed

| Tool | Description |
|---|---|
| `golusoris_init` | Scaffold a new app |
| `golusoris_add` | Show how to add a module |
| `golusoris_bump` | Show how to bump golusoris version |

## MCP client config (Claude Code)

```json
{
  "mcpServers": {
    "golusoris": {
      "command": "golusoris-mcp",
      "args": ["--addr", ":8899"]
    }
  }
}
```

## Don't

- Don't add tools that shell out to arbitrary commands — keep tool output
  as instructions to the agent, not side effects.
