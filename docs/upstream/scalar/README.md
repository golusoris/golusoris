# Scalar API reference UI — snapshot

Source: https://scalar.com / https://github.com/scalar/scalar
Used via: embedded JS in `apidocs/` handler

## Usage in golusoris

```go
// apidocs/ mounts:
//   GET /docs        → Scalar UI (HTML page loading the JS bundle)
//   GET /mcp         → MCP-from-OpenAPI tool list
//   GET /openapi.json → spec passthrough

// Mount alongside ogen server:
r.Mount("/docs", apidocs.Handler(apidocs.Config{
    SpecURL: "/openapi.json",
    Title:   "My API",
}))
```

## Scalar HTML embed pattern

```html
<!doctype html>
<html>
<head><title>API Reference</title></head>
<body>
  <script
    id="api-reference"
    data-url="/openapi.json"
    src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
</body>
</html>
```

In production the JS is embedded via `embed.FS` to avoid external CDN dependency.

## Configuration options

```js
document.getElementById('api-reference').dataset.configuration = JSON.stringify({
  theme: 'default',         // 'default' | 'dark' | 'solarized' | etc.
  layout: 'modern',         // 'modern' | 'classic'
  hideModels: false,
  hideDownloadButton: false,
  showSidebar: true,
})
```

## Links

- Docs: https://github.com/scalar/scalar/tree/main/packages/api-reference
- Themes: https://github.com/scalar/scalar/tree/main/packages/themes
