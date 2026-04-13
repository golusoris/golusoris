// Package apidocs mounts an OpenAPI-driven docs UI (Scalar) and an MCP
// server (Model Context Protocol over HTTP) so AI agents can call the app's
// operations as tools.
//
// Scalar bundle is embedded — zero runtime dependency on a CDN. The MCP
// endpoint parses the OpenAPI spec and maps each operation to an MCP tool.
//
// Wiring:
//
//	fx.New(
//	    golusoris.Core,
//	    golusoris.HTTP,
//	    apidocs.Module,
//	    fx.Supply(apidocs.Options{
//	        Title:   "My API",
//	        Spec:    mySpec,        // []byte, YAML or JSON
//	        BaseURL: "http://localhost:8080",
//	    }),
//	)
//
// The module mounts handlers on the injected chi.Router at:
//
//	GET /docs          → Scalar UI
//	GET /docs/scalar.js → embedded Scalar bundle
//	GET /openapi.yaml  → raw OpenAPI spec (or .json — sniffed from Spec)
//	POST /mcp          → MCP JSON-RPC 2.0 endpoint
package apidocs

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
)

// Options configures the apidocs handlers. All fields are required except
// where noted.
type Options struct {
	// Title is the display title shown in the Scalar UI + MCP server info.
	Title string
	// Spec is the OpenAPI 3.x document (YAML or JSON bytes). Required.
	Spec []byte
	// BaseURL is the app's externally reachable base URL (e.g.
	// "https://api.example.com"). Used by the MCP tools/call handler to
	// build outbound requests. Required for /mcp to function.
	BaseURL string
	// ServerName is the MCP server identifier. Defaults to Title.
	ServerName string
	// ServerVersion is the MCP server version. Defaults to "0.0.0".
	ServerVersion string
	// HTTPClient is the client used by /mcp tools/call. Callers should pass
	// an httpx/client with retry + breaker. nil uses http.DefaultClient.
	HTTPClient *http.Client
}

// Mount attaches the apidocs handlers to r. Typically called via [Module].
func Mount(r chi.Router, opts Options) error {
	if len(opts.Spec) == 0 {
		return errors.New("apidocs: Options.Spec is required")
	}
	if opts.ServerName == "" {
		opts.ServerName = opts.Title
	}
	if opts.ServerVersion == "" {
		opts.ServerVersion = "0.0.0"
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}

	specHandler, specPath := specServeHandler(opts.Spec)

	r.Get("/docs", scalarHTMLHandler(opts.Title, specPath))
	r.Get("/docs/scalar.js", scalarJSHandler())
	r.Get(specPath, specHandler)

	mcpHandler, err := newMCPHandler(opts)
	if err != nil {
		return err
	}
	r.Post("/mcp", mcpHandler)
	return nil
}

// Module mounts the apidocs handlers on the injected chi.Router during fx
// Start. Fails the app's startup if Options.Spec is missing/unparseable.
var Module = fx.Module("golusoris.apidocs",
	fx.Invoke(Mount),
)
