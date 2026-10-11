<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Scalar API reference UI — v1.73.1 snapshot

Pinned: **v1.73.1**
Source: [published package v1.73.1](https://www.npmjs.com/package/@scalar/api-reference/v/1.73.1)
Used via: `apidocs/embed/scalar.js`; version authority is
`apidocs/embed/SCALAR_VERSION` and the bundle header.

## Usage in golusoris

```go
err := apidocs.Mount(r, apidocs.Options{
    Title: "My API",
    Spec:  openAPISpec,
})
if err != nil {
    return fmt.Errorf("mount API docs: %w", err)
}
```

`apidocs.Module` is the Fx alternative: supply the same `apidocs.Options` and
include the module. Mounting adds `/docs`, `/docs/scalar.js`, and
`/openapi.json` or `/openapi.yaml` according to the supplied spec.

MCP remains absent by default. Set `EnableMCP`, `BaseURL`, and a per-request
`MCPAuthorize` callback to mount authenticated `/mcp`. Supply `HTTPClient` when
the bounded framework default is not suitable.

## Scalar HTML embed pattern

```html
<!doctype html>
<html>
<head><title>API Reference</title></head>
<body>
  <script
    id="api-reference"
    data-url="/openapi.json"
    data-configuration='{"telemetry":false}'></script>
  <script src="/docs/scalar.js"></script>
</body>
</html>
```

The shipped handler always serves the embedded bundle, so the script itself has
no runtime CDN dependency. The bundle's `telemetry` setting defaults to on from
v1.73; the handler turns it off. With its default fonts the page still requests
font files from `fonts.scalar.com`, as it did with v1.25.52.

## Links

- [Published package](https://www.npmjs.com/package/@scalar/api-reference/v/1.73.1)
- [Scalar documentation](https://guides.scalar.com/scalar/scalar-api-references)
