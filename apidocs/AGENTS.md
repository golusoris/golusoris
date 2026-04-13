# Agent guide — apidocs

Mounts `/docs` (Scalar UI) and `/mcp` (MCP-from-OpenAPI) on the injected chi router.

## Endpoints

| Path | Method | Serves |
|---|---|---|
| `/docs` | GET | Scalar HTML wrapper |
| `/docs/scalar.js` | GET | embedded Scalar bundle (pinned, immutable-cached) |
| `/openapi.yaml` or `/openapi.json` | GET | raw spec (content-type sniffed from Spec bytes) |
| `/mcp` | POST | JSON-RPC 2.0 endpoint (MCP streamable HTTP stateless) |

## MCP coverage

Implemented: `initialize`, `tools/list`, `tools/call`. Each OpenAPI operation becomes a tool:
- name: `operationId` (fallback: `<method>_<sanitized_path>`)
- description: `summary`, falling back to `description`
- inputSchema: parameters merged into a JSON Schema `object`, `body` nested when the operation has a JSON request body

`tools/call` builds an outbound HTTP request to `Options.BaseURL` using:
- Path param substitution for arguments matching `{name}` in the spec path
- Remaining scalar arguments as query string
- `"body"` argument as the JSON request body

Not implemented: `resources/*`, `prompts/*`, server-sent streaming, subscriptions. Apps needing those should layer their own MCP SDK (see Step 20's `cmd/golusoris-mcp`).

## Scalar bundle

`apidocs/embed/scalar.js` is pinned to a specific `@scalar/api-reference` version. Update via `make scalar-update VERSION=1.x.y`.

## Don't

- Don't expose `/mcp` publicly without auth if the tools modify state. MCP tools are a remote-call surface — gate by the same middleware as the underlying API.
- Don't ship apidocs without a `BaseURL` set — `tools/call` will refuse with `-32603` until it's provided.
