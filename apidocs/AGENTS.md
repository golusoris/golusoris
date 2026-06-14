# Agent guide — apidocs

Mounts `/docs` (Scalar UI) and `/mcp` (MCP-from-OpenAPI) on the injected chi router.

## Endpoints

| Path | Method | Serves |
|---|---|---|
| `/docs` | GET | Scalar HTML wrapper |
| `/docs/scalar.js` | GET | embedded Scalar bundle (pinned, immutable-cached) |
| `/openapi.yaml` or `/openapi.json` | GET | raw spec (content-type sniffed from Spec bytes) |
| `/mcp` | GET/POST/DELETE | MCP server — official go-sdk, streamable-HTTP transport |

## MCP coverage

Built on the official `github.com/modelcontextprotocol/go-sdk` (streamable-HTTP
transport — initialize handshake, sessions, SSE). Each OpenAPI operation is
registered as a tool:
- name: `operationId` (fallback: `<method>_<sanitized_path>`)
- description: `summary`, falling back to `description`
- inputSchema: parameters merged into a JSON Schema `object`, `body` nested when the operation has a JSON request body

`tools/call` builds an outbound HTTP request to `Options.BaseURL` using:
- Path param substitution for arguments matching `{name}` in the spec path
- Remaining scalar arguments as query string
- `"body"` argument as the JSON request body

Two SSRF guards on the constructed URL (kept from the pre-SDK handler): a tight
URL-safe charset regexp (`toolPathRE`) and `safeResolveURL` pinning scheme+host
to `BaseURL`. Resources/prompts/sampling become available from the SDK if the
server is extended.

## Scalar bundle

`apidocs/embed/scalar.js` is pinned to a specific `@scalar/api-reference` version. Update via `make scalar-update VERSION=1.x.y`.

## Don't

- Don't expose `/mcp` publicly without auth if the tools modify state. MCP tools are a remote-call surface — gate by the same middleware as the underlying API.
- Don't ship apidocs without a `BaseURL` set — `tools/call` returns an MCP error result ("BaseURL is unset; tool calls disabled") until it's provided.
