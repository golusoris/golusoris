package apidocs

import (
	"bytes"
	_ "embed"
	"fmt"
	"html"
	"net/http"
)

// scalarJS is the standalone Scalar API reference bundle. Pinned version in
// apidocs/embed/SCALAR_VERSION. Update via: make scalar-update.
//
//go:embed embed/scalar.js
var scalarJS []byte

// scalarHTMLTemplate is the one-page HTML wrapper Scalar expects. The
// <script id="api-reference"> tag carries a data-url attribute pointing to
// the OpenAPI spec, and the <script src> tag loads the bundle.
const scalarHTMLTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
</head>
<body>
<script id="api-reference" data-url="%s"></script>
<script src="/docs/scalar.js"></script>
</body>
</html>
`

func scalarHTMLHandler(title, specPath string) http.HandlerFunc {
	body := fmt.Sprintf(scalarHTMLTemplate, html.EscapeString(title), html.EscapeString(specPath))
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

func scalarJSHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(scalarJS)
	}
}

// specServeHandler returns a handler that serves the raw OpenAPI spec + the
// path it should be mounted at. Path is "/openapi.json" if Spec parses as
// JSON, else "/openapi.yaml".
func specServeHandler(spec []byte) (http.HandlerFunc, string) {
	path := "/openapi.yaml"
	contentType := "application/yaml"
	if isJSON(spec) {
		path = "/openapi.json"
		contentType = "application/json"
	}
	h := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(spec)
	}
	return h, path
}

func isJSON(b []byte) bool {
	trimmed := bytes.TrimSpace(b)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}
