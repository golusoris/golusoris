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

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
var toolPathRE = regexp.MustCompile(`^/[A-Za-z0-9/_.~\-%?&=+]*$`)

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
}

// newMCPHandler builds the streamable-HTTP MCP handler: it derives one tool
// per OpenAPI operation and registers a proxy handler for each.
func newMCPHandler(opts Options) (http.Handler, error) {
	tools, err := openAPIToTools(opts.Spec)
	if err != nil {
		return nil, fmt.Errorf("apidocs/mcp: build tools: %w", err)
	}
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
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil), nil
}

// proxyHandler forwards a tools/call invocation to the live API operation,
// with two layers of SSRF sanitization on the constructed URL.
func proxyHandler(opts Options, tool Tool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if opts.BaseURL == "" {
			return toolError("apidocs: BaseURL is unset; tool calls disabled"), nil
		}
		path, body, contentType, err := buildCall(&tool, req.Params.Arguments)
		if err != nil {
			return toolError(err.Error()), nil
		}
		//   1) toolPathRE rejects anything outside a tight URL-safe charset —
		//      user args are url.PathEscape'd in buildCall so malformed input
		//      indicates a bug, not a benign edge case.
		//   2) safeResolveURL pins the result's scheme+host to opts.BaseURL.
		if !toolPathRE.MatchString(path) {
			return toolError("apidocs: tool path contains disallowed characters"), nil
		}
		callURL, err := safeResolveURL(opts.BaseURL, path)
		if err != nil {
			return toolError(err.Error()), nil
		}
		httpReq, err := http.NewRequestWithContext(ctx, tool.method, callURL, body)
		if err != nil {
			return toolError("build request: " + err.Error()), nil
		}
		if contentType != "" {
			httpReq.Header.Set("Content-Type", contentType)
		}

		resp, err := opts.HTTPClient.Do(httpReq)
		if err != nil {
			return toolError("request failed: " + err.Error()), nil
		}
		defer func() { _ = resp.Body.Close() }()

		bodyBytes, _ := io.ReadAll(resp.Body)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(bodyBytes)),
			}},
			IsError: resp.StatusCode >= 400,
		}, nil
	}
}

// toolError reports a tool-level failure as an MCP IsError result (visible to
// the model) rather than a transport error.
func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
		IsError: true,
	}
}

// safeResolveURL joins a path onto a BaseURL and asserts the resulting URL
// stays within the base's scheme+host+port. Returns the final URL string or
// an error if the path would redirect to a different origin.
func safeResolveURL(baseURL, path string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("apidocs: invalid BaseURL %q", baseURL)
	}
	ref, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("apidocs: invalid tool path: %w", err)
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme != base.Scheme || resolved.Host != base.Host {
		return "", errors.New("apidocs: tool path escapes BaseURL origin")
	}
	return resolved.String(), nil
}
