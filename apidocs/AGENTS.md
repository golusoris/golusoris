<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — apidocs

Mounts `/docs` (Scalar UI) and opt-in `/mcp` (MCP-from-OpenAPI) on injected chi
router.

## Endpoints

| Path | Method | Serves |
| --- | --- | --- |
| `/docs` | GET | Scalar HTML wrapper |
| `/docs/scalar.js` | GET | embedded Scalar bundle |
| `/openapi.yaml` or `/openapi.json` | GET | raw spec |
| `/mcp` | GET/POST/DELETE | opt-in authenticated MCP |

## MCP coverage

Built on official `github.com/modelcontextprotocol/go-sdk` (streamable-HTTP
transport — initialize handshake, sessions, SSE). Each OpenAPI operation is
registered as tool:

- name: `operationId` (fallback: `<method>_<sanitized_path>`)
- duplicate derived or explicit names reject with both method/path origins
- description: `summary`, falling back to `description`
- inputSchema: path-item plus operation parameters merged into JSON Schema
  `object`; operation entries override matching path-item entries; `body` holds
  JSON request body
- tool order: name, then method, then path

`tools/call` builds outbound HTTP request to `Options.BaseURL` using:

- path, query, header, and cookie placement from OpenAPI `in`
- required/type/enum/pattern/range/additional-property validation before I/O
- OpenAPI `style` and `explode` serialization; deterministic object keys
- parameter arrays/objects capped at 256 items; nested parameter values reject
- exact `.` and `..` path-parameter segments rejected before URL resolution
- `"body"` argument with selected OpenAPI JSON or `+json` media type
- 1 MiB upstream response-body limit

Two SSRF guards on constructed URL (kept from pre-SDK handler): tight
URL-safe charset regexp (`toolPathRE`) and `safeResolveURL` pinning scheme+host
to HTTP(S) `BaseURL`. Redirects remain on same origin, stop at ten hops, then
apply caller policy. Userinfo rejected.

Injected clients are cloned. Positive timeout stays. Zero timeout becomes 30s.

`/mcp` absent by default. Enable with `Options.EnableMCP`; also set
`Options.MCPAuthorize`. Mount fails for enabled MCP without authorizer or valid
HTTP(S) `BaseURL`.

MCP input names must stay unique across parameter locations; `body` reserved
for declared JSON request body. These constraints apply only when MCP enabled;
docs-only mounting validates OpenAPI without MCP tool conversion.

## Scalar bundle

`apidocs/embed/scalar.js` pins `@scalar/api-reference`. Update via
`make scalar-update VERSION=1.x.y`.

## Don't

- Don't make `MCPAuthorize` permissive outside explicit trusted deployments.
- Don't enable MCP without `BaseURL`.
- Don't create bare `*http.Client` without `Timeout`.
- Don't use `http.DefaultClient`. Outbound HTTP flows through parent `httpx` or
  `extclient` package: timeout, retry, circuit breaker, OTel.
