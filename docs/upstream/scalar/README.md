<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Scalar API reference UI — v1.76.3 snapshot

Pinned: **v1.76.3**
Source: [published package v1.76.3](https://www.npmjs.com/package/@scalar/api-reference/v/1.76.3)
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
    data-configuration='{"telemetry":false,"withDefaultFonts":false}'></script>
  <script src="/docs/scalar.js"></script>
</body>
</html>
```

The shipped handler serves the embedded bundle and turns off the two settings
that reach outside: `telemetry` (on by default from v1.73) and the default
fonts, which load from `fonts.scalar.com`. The page uses system fonts and
contacts no outside host; `TestServedPageContactsNoOutsideHost` renders it in
Chrome and reads the network log to prove it.

## Links

- [Published package](https://www.npmjs.com/package/@scalar/api-reference/v/1.76.3)
- [Scalar documentation](https://guides.scalar.com/scalar/scalar-api-references)
