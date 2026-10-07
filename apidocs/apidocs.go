// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package apidocs mounts an OpenAPI-driven docs UI (Scalar) and can mount an
// authenticated MCP server (Model Context Protocol over HTTP) so AI agents can
// call the app's operations as tools.
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
//	        Title:        "My API",
//	        Spec:         mySpec, // []byte, YAML or JSON
//	        BaseURL:      "http://localhost:8080",
//	        EnableMCP:    true,
//	        MCPAuthorize: authorizeMCPRequest,
//	    }),
//	)
//
// The module mounts docs handlers on the injected chi.Router at:
//
//	GET /docs          → Scalar UI
//	GET /docs/scalar.js → embedded Scalar bundle
//	GET /openapi.yaml  → raw OpenAPI spec (or .json — sniffed from Spec)
//	/mcp               → opt-in MCP server (official go-sdk, streamable HTTP)
package apidocs

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"

	httpclient "github.com/golusoris/golusoris/httpx/client"
)

const defaultMCPRequestTimeout = 30 * time.Second

// Options configures the apidocs handlers. Spec is always required. MCP fields
// apply only when EnableMCP is true.
type Options struct {
	// Title is the display title shown in the Scalar UI + MCP server info.
	Title string
	// Spec is the OpenAPI 3.x document (YAML or JSON bytes). Required.
	Spec []byte
	// BaseURL is the app's externally reachable base URL (e.g.
	// "https://api.example.com"). Used by the MCP tools/call handler to
	// build outbound requests. Required when EnableMCP is true.
	BaseURL string
	// ServerName is the MCP server identifier. Defaults to Title.
	ServerName string
	// ServerVersion is the MCP server version. Defaults to "0.0.0".
	ServerVersion string
	// HTTPClient is cloned for /mcp tools/call. A non-positive timeout becomes
	// 30s. nil uses the bounded framework default.
	HTTPClient *http.Client
	// EnableMCP explicitly enables the /mcp remote-call surface. It is disabled
	// by default.
	EnableMCP bool
	// MCPAuthorize authenticates every request before it reaches the MCP
	// transport. Required when EnableMCP is true.
	MCPAuthorize func(*http.Request) bool
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

	mcpMount, err := buildOptionalMCPHandler(opts)
	if err != nil {
		return err
	}

	specHandler, specPath := specServeHandler(opts.Spec)

	r.Get("/docs", scalarHTMLHandler(opts.Title, specPath))
	r.Get("/docs/scalar.js", scalarJSHandler())
	r.Get(specPath, specHandler)

	if mcpMount.enabled {
		r.Handle("/mcp", mcpMount.handler)
	}
	return nil
}

type optionalMCPMount struct {
	handler http.Handler
	enabled bool
}

func buildOptionalMCPHandler(opts Options) (optionalMCPMount, error) {
	if !opts.EnableMCP {
		if err := validateOpenAPISpec(opts.Spec); err != nil {
			return optionalMCPMount{}, fmt.Errorf("apidocs: %w", err)
		}
		return optionalMCPMount{}, nil
	}
	if opts.MCPAuthorize == nil {
		return optionalMCPMount{}, errors.New("apidocs: Options.MCPAuthorize is required when EnableMCP is true")
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = httpclient.New(httpclient.Options{Name: "golusoris.apidocs"})
	}
	confinedClient, err := sameOriginClient(opts.HTTPClient, opts.BaseURL)
	if err != nil {
		return optionalMCPMount{}, err
	}
	opts.HTTPClient = confinedClient
	tools, err := openAPIToTools(opts.Spec)
	if err != nil {
		return optionalMCPMount{}, fmt.Errorf("apidocs/mcp: build tools: %w", err)
	}
	return optionalMCPMount{
		handler: authorizeMCP(opts.MCPAuthorize, newMCPHandler(opts, tools)),
		enabled: true,
	}, nil
}

// Module mounts the apidocs handlers on the injected chi.Router during fx
// Start. Fails the app's startup if Options.Spec is missing/unparseable.
var Module = fx.Module(
	"golusoris.apidocs",
	fx.Invoke(Mount),
)
