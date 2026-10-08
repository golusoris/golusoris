// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apidocs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	httpclient "github.com/golusoris/golusoris/httpx/client"
)

// MCP server built on the official Go SDK
// (github.com/modelcontextprotocol/go-sdk). Each OpenAPI operation becomes an
// MCP tool; tools/call proxies to the live API described by Options.BaseURL.
// The handler speaks the full streamable-HTTP transport (initialize handshake,
// session, SSE) rather than the prior hand-rolled stateless JSON-RPC subset —
// so apps now get resources/prompts/protocol-negotiation for free if they
// extend the server.

// toolPathRE bounds the set of characters that can appear in a tool-call
// path. buildCall url.PathEscape's every user-supplied arg, so any char
// outside this set is a bug upstream — fail closed rather than forward the
// request. This is the static sanitizer CodeQL's request-forgery query
// recognizes for the opts.HTTPClient.Do(...) sink below.
var toolPathRE = regexp.MustCompile(`^/[A-Za-z0-9/_.~\-%,;?&=+]*$`)

const maxMCPResponseBodyBytes int64 = 1 << 20

var errRedirectEscapesBaseOrigin = errors.New("apidocs: redirect escapes BaseURL origin")

// Tool is an OpenAPI operation mapped to an MCP tool. Exported so callers and
// tests can inspect the derived catalog. The method/path fields describe how
// tools/call reaches the live API.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// Internal fields (not serialized over MCP) describing how to invoke the
	// tool via HTTP. Populated by openAPIToTools.
	method string
	path   string
	params []toolParameter
	// bodyContentType is the JSON-compatible OpenAPI media type selected for
	// the request body schema.
	bodyContentType string
	inputValidator  *openapi3.Schema
	jsonSchema2020  bool
}

// newMCPHandler builds the streamable-HTTP MCP handler: it derives one tool
// per OpenAPI operation and registers a proxy handler for each.
func newMCPHandler(opts Options, tools []Tool) http.Handler {
	srv := mcp.NewServer(
		&mcp.Implementation{Name: opts.ServerName, Version: opts.ServerVersion},
		nil,
	)
	for i := range tools {
		t := tools[i]
		srv.AddTool(
			&mcp.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema},
			proxyHandler(opts, t),
		)
	}
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
}

// proxyHandler forwards a tools/call invocation to the live API operation,
// with two layers of SSRF sanitization on the constructed URL.
func proxyHandler(opts Options, tool Tool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
		httpReq, errRes := validatedProxyRequest(ctx, opts, tool, req.Params.Arguments)
		if errRes != nil {
			return errRes, nil
		}

		resp, err := opts.HTTPClient.Do(httpReq)
		if err != nil {
			return toolError("request failed: " + err.Error()), nil
		}
		// Closure rather than a bare defer so bodyclose sees the Body reach a
		// closer. A close failure is reported like every other failure in this
		// handler — as an IsError tool result. Returning it as the handler's
		// error would make the go-sdk emit a JSON-RPC protocol error instead.
		defer func() {
			if cerr := resp.Body.Close(); cerr != nil && err == nil {
				res = toolError("apidocs: close response body: " + cerr.Error())
			}
		}()

		bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxMCPResponseBodyBytes+1))
		if err != nil {
			return toolError("read response: " + err.Error()), nil
		}
		if int64(len(bodyBytes)) > maxMCPResponseBodyBytes {
			return toolError(fmt.Sprintf("apidocs: response body exceeds %d bytes", maxMCPResponseBodyBytes)), nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(bodyBytes)),
			}},
			IsError: resp.StatusCode >= 400,
		}, nil
	}
}

func validatedProxyRequest(
	ctx context.Context,
	opts Options,
	tool Tool,
	args json.RawMessage,
) (*http.Request, *mcp.CallToolResult) {
	if opts.BaseURL == "" {
		return nil, toolError("apidocs: BaseURL is unset; tool calls disabled")
	}
	if err := validateToolArguments(tool, args); err != nil {
		return nil, toolError("apidocs: invalid tool arguments: " + err.Error())
	}
	return buildProxyRequest(ctx, opts, tool, args)
}

func validateToolArguments(tool Tool, raw json.RawMessage) error {
	if tool.inputValidator == nil {
		return errors.New("input validator is unavailable")
	}
	var value any = map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("decode JSON: %w", err)
		}
	}
	if tool.jsonSchema2020 {
		if err := tool.inputValidator.VisitJSON(value, openapi3.EnableJSONSchema2020()); err != nil {
			return fmt.Errorf("validate OpenAPI 3.1 schema: %w", err)
		}
		return nil
	}
	if err := tool.inputValidator.VisitJSON(value); err != nil {
		return fmt.Errorf("validate OpenAPI schema: %w", err)
	}
	return nil
}

// buildProxyRequest resolves and builds the outbound *http.Request for a
// tools/call invocation. On failure it returns a nil request and an IsError
// tool result describing why the request couldn't be built — never a Go
// error, so the go-sdk reports it as a tool failure rather than a
// JSON-RPC protocol error.
//
//  1. toolPathRE rejects anything outside a tight URL-safe charset — user
//     args are url.PathEscape'd in buildCall so malformed input indicates a
//     bug, not a benign edge case.
//  2. safeResolveURL pins the result's scheme+host to opts.BaseURL.
func buildProxyRequest(ctx context.Context, opts Options, tool Tool, args json.RawMessage) (*http.Request, *mcp.CallToolResult) {
	call, err := buildCall(&tool, args)
	if err != nil {
		return nil, toolError(err.Error())
	}
	if !toolPathRE.MatchString(call.path) {
		return nil, toolError("apidocs: tool path contains disallowed characters")
	}
	callURL, err := safeResolveURL(opts.BaseURL, call.path)
	if err != nil {
		return nil, toolError(err.Error())
	}
	httpReq, err := http.NewRequestWithContext(ctx, tool.method, callURL, call.body)
	if err != nil {
		return nil, toolError("build request: " + err.Error())
	}
	httpReq.Header = call.headers
	if call.contentType != "" {
		httpReq.Header.Set("Content-Type", call.contentType)
	}
	for i := range len(call.cookies) {
		httpReq.AddCookie(&call.cookies[i])
	}
	return httpReq, nil
}

// toolError reports a tool-level failure as an MCP IsError result (visible to
// the model) rather than a transport error.
func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}

func authorizeMCP(authorize func(*http.Request) bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !authorize(req) {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func sameOriginClient(client *http.Client, rawBaseURL string) (*http.Client, error) {
	base, err := parseBaseURL(rawBaseURL)
	if err != nil {
		return nil, err
	}
	clone := httpclient.CloneBounded(client, defaultMCPRequestTimeout)
	callerCheckRedirect := clone.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !sameOrigin(base, req.URL) {
			return errRedirectEscapesBaseOrigin
		}
		if len(via) >= 10 {
			return errors.New("apidocs: stopped after 10 redirects")
		}
		if callerCheckRedirect != nil {
			return callerCheckRedirect(req, via)
		}
		return nil
	}
	return clone, nil
}

func parseBaseURL(rawBaseURL string) (*url.URL, error) {
	base, err := url.Parse(rawBaseURL)
	if err != nil || base.Hostname() == "" || base.User != nil || !isHTTPScheme(base.Scheme) {
		return nil, fmt.Errorf("apidocs: invalid BaseURL %q", rawBaseURL)
	}
	return base, nil
}

func isHTTPScheme(scheme string) bool {
	return strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https")
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || a.User != nil || b.User != nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if strings.EqualFold(value.Scheme, "http") {
		return "80"
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return ""
}

// safeResolveURL joins a path onto a BaseURL and asserts the resulting URL
// stays within the base's scheme+host+port. The BaseURL path is a deployment
// prefix, so it remains ahead of every OpenAPI operation path.
func safeResolveURL(baseURL, path string) (string, error) {
	base, err := parseBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("apidocs: invalid tool path: %w", err)
	}
	if ref.IsAbs() || ref.Host != "" {
		return "", errors.New("apidocs: tool path escapes BaseURL origin")
	}
	resolved := base.JoinPath(ref.EscapedPath())
	resolved.RawQuery = ref.RawQuery
	resolved.ForceQuery = ref.ForceQuery
	resolved.Fragment = ""
	resolved.RawFragment = ""
	if !sameOrigin(base, resolved) {
		return "", errors.New("apidocs: tool path escapes BaseURL origin")
	}
	return resolved.String(), nil
}
