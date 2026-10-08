// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apidocs_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/golusoris/golusoris/apidocs"
)

const minimalSpec = `openapi: 3.0.3
info:
  title: Example
  version: "1"
paths:
  /echo/{id}:
    get:
      operationId: getEcho
      summary: echo an id
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        '200':
          description: ok
  /things:
    post:
      operationId: createThing
      summary: create a thing
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
      responses:
        '201':
          description: created
`

const validationSpec = `openapi: 3.0.3
info:
  title: Validation
  version: "1"
paths:
  /validated/{id}:
    get:
      operationId: validateArguments
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
            pattern: '^[a-z]+$'
        - name: mode
          in: query
          required: true
          schema:
            type: string
            enum: [safe]
        - name: count
          in: query
          required: true
          schema:
            type: integer
            minimum: 1
            maximum: 3
      responses:
        '200':
          description: ok
`

func mount(t *testing.T, baseURL string) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	if err := apidocs.Mount(r, apidocs.Options{
		Title:   "Example",
		Spec:    []byte(minimalSpec),
		BaseURL: baseURL,
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return r
}

func mountWithSpec(t *testing.T, spec []byte, baseURL string, hc *http.Client) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	if err := apidocs.Mount(r, apidocs.Options{
		Title:         "Example",
		Spec:          spec,
		BaseURL:       baseURL,
		ServerName:    "example",
		ServerVersion: "0.0.1",
		HTTPClient:    hc,
		EnableMCP:     true,
		MCPAuthorize:  func(*http.Request) bool { return true },
	}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	return r
}

func TestMountRejectsMissingSpec(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	err := apidocs.Mount(r, apidocs.Options{})
	if err == nil {
		t.Fatal("expected error for missing Spec")
	}
}

func TestMCPDisabledByDefault(t *testing.T) {
	t.Parallel()
	r := mount(t, "http://example.test")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("POST /mcp status = %d, want 404", rr.Code)
	}
}

func TestDisabledMCPAcceptsLocationAmbiguousParameters(t *testing.T) {
	t.Parallel()
	spec := []byte(`openapi: 3.0.3
info:
  title: Docs only
  version: "1"
paths:
  /items:
    get:
      parameters:
        - {name: token, in: query, schema: {type: string}}
        - {name: token, in: header, schema: {type: string}}
      responses:
        '200': {description: ok}
`)
	r := chi.NewRouter()
	if err := apidocs.Mount(r, apidocs.Options{Spec: spec}); err != nil {
		t.Fatalf("docs-only Mount: %v", err)
	}
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /openapi.yaml status = %d, want 200", rr.Code)
	}
}

func TestMCPRequiresAuthorizer(t *testing.T) {
	t.Parallel()
	err := apidocs.Mount(chi.NewRouter(), apidocs.Options{
		Spec:      []byte(minimalSpec),
		BaseURL:   "http://example.test",
		EnableMCP: true,
	})
	if err == nil || !strings.Contains(err.Error(), "MCPAuthorize") {
		t.Fatalf("Mount error = %v, want missing MCPAuthorize", err)
	}
}

func TestMCPAuthorizerRejectsUnauthorizedRequests(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	err := apidocs.Mount(r, apidocs.Options{
		Spec:      []byte(minimalSpec),
		BaseURL:   "http://example.test",
		EnableMCP: true,
		MCPAuthorize: func(req *http.Request) bool {
			return req.Header.Get("Authorization") == "Bearer allowed"
		},
	})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	unauthorized := httptest.NewRecorder()
	r.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", unauthorized.Code)
	}

	authorizedReq := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	authorizedReq.Header.Set("Authorization", "Bearer allowed")
	authorized := httptest.NewRecorder()
	r.ServeHTTP(authorized, authorizedReq)
	if authorized.Code == http.StatusUnauthorized {
		t.Fatal("authorized request was rejected")
	}
}

func TestScalarDocsServesHTMLAndJS(t *testing.T) {
	t.Parallel()
	r := mount(t, "http://example.test")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/docs status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `data-url="/openapi.yaml"`) {
		t.Errorf("HTML missing data-url: %q", rr.Body.String())
	}

	rr = httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/docs/scalar.js", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("/docs/scalar.js status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/javascript" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rr.Body.Len() < 1000 {
		t.Errorf("scalar.js unexpectedly small: %d bytes", rr.Body.Len())
	}
}

func TestOpenAPISpecServedAtCorrectPath(t *testing.T) {
	t.Parallel()
	r := mount(t, "http://example.test")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// mcpSession mounts the apidocs router behind a real HTTP server and connects
// an MCP client over the streamable-HTTP transport, returning the live session.
func mcpSession(t *testing.T, baseURL string) *mcp.ClientSession {
	t.Helper()
	return mcpSessionWith(t, baseURL, nil)
}

// mcpSessionWith is mcpSession with an explicit outbound client for the
// tools/call proxy.
func mcpSessionWith(t *testing.T, baseURL string, hc *http.Client) *mcp.ClientSession {
	t.Helper()
	return mcpSessionWithSpec(t, []byte(minimalSpec), baseURL, hc)
}

func mcpSessionWithSpec(t *testing.T, spec []byte, baseURL string, hc *http.Client) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(mountWithSpec(t, spec, baseURL, hc))
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: srv.Client()}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestMCPToolsCallRejectsSchemaInvalidArgumentsBeforeUpstream(t *testing.T) {
	t.Parallel()
	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cs := mcpSessionWithSpec(t, []byte(validationSpec), upstream.URL, nil)
	tests := []struct {
		name string
		args map[string]any
	}{
		{name: "missing required", args: map[string]any{"id": "abc", "count": 2}},
		{name: "extra property", args: map[string]any{"id": "abc", "mode": "safe", "count": 2, "extra": true}},
		{name: "wrong type", args: map[string]any{"id": "abc", "mode": "safe", "count": "2"}},
		{name: "enum", args: map[string]any{"id": "abc", "mode": "unsafe", "count": 2}},
		{name: "pattern", args: map[string]any{"id": "ABC", "mode": "safe", "count": 2}},
		{name: "range", args: map[string]any{"id": "abc", "mode": "safe", "count": 4}},
	}
	for _, test := range tests {
		before := upstreamCalls.Load()
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "validateArguments",
			Arguments: test.args,
		})
		if err != nil {
			t.Fatalf("%s: call tool: %v", test.name, err)
		}
		if !res.IsError || !resultContains(res, "invalid tool arguments") {
			t.Fatalf("%s: result = %+v, want invalid-arguments tool error", test.name, res)
		}
		if got := upstreamCalls.Load(); got != before {
			t.Fatalf("%s: upstream calls = %d, want %d", test.name, got, before)
		}
	}
}

func TestMCPInitialize(t *testing.T) {
	t.Parallel()
	cs := mcpSession(t, "http://example.test")
	info := cs.InitializeResult()
	if info.ServerInfo == nil || info.ServerInfo.Name != "example" {
		t.Errorf("server info = %+v, want name %q", info.ServerInfo, "example")
	}
}

func TestMCPToolsList(t *testing.T) {
	t.Parallel()
	cs := mcpSession(t, "http://example.test")
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"getEcho", "createThing"} {
		if !names[want] {
			t.Errorf("tools/list missing %q; got %v", want, names)
		}
	}
}

// TestMCPToolsCallProxiesRequest proves tools/call builds the right outbound
// request (path substitution, method) and returns the upstream reply.
func TestMCPToolsCallProxiesRequest(t *testing.T) {
	t.Parallel()
	var hitPath atomic.Value
	hitPath.Store("")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hitPath.Store(req.URL.Path)
		_, _ = io.WriteString(w, `{"echoed":true}`)
	}))
	defer upstream.Close()

	cs := mcpSession(t, upstream.URL)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "getEcho",
		Arguments: map[string]any{"id": "abc123"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Errorf("IsError=true: %+v", res.Content)
	}
	if p := hitPath.Load().(string); p != "/echo/abc123" {
		t.Errorf("upstream hit %q, want /echo/abc123", p)
	}
	if !resultContains(res, "echoed") {
		t.Errorf("result content = %+v", res.Content)
	}
}

// TestMCPToolsCallPostsBody proves that a tool with a requestBody pushes the
// JSON body upstream.
func TestMCPToolsCallPostsBody(t *testing.T) {
	t.Parallel()
	var gotBody atomic.Value
	gotBody.Store("")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		gotBody.Store(string(b))
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()

	cs := mcpSession(t, upstream.URL)
	if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "createThing",
		Arguments: map[string]any{"body": map[string]any{"name": "widget"}},
	}); err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if body := gotBody.Load().(string); !strings.Contains(body, `"widget"`) {
		t.Errorf("upstream body = %q", body)
	}
}

func TestMCPMountRejectsMissingBaseURL(t *testing.T) {
	t.Parallel()
	err := apidocs.Mount(chi.NewRouter(), apidocs.Options{
		Spec:         []byte(minimalSpec),
		EnableMCP:    true,
		MCPAuthorize: func(*http.Request) bool { return true },
	})
	if err == nil || !strings.Contains(err.Error(), "BaseURL") {
		t.Fatalf("Mount error = %v, want missing BaseURL", err)
	}
}

func TestMCPMountRejectsUnsafeBaseURLs(t *testing.T) {
	t.Parallel()
	for _, baseURL := range []string{"ftp://api.test", "https://user:secret@api.test", "http://:8080"} {
		t.Run(baseURL, func(t *testing.T) {
			t.Parallel()
			err := apidocs.Mount(chi.NewRouter(), apidocs.Options{
				Spec:         []byte(minimalSpec),
				BaseURL:      baseURL,
				EnableMCP:    true,
				MCPAuthorize: func(*http.Request) bool { return true },
			})
			if err == nil || !strings.Contains(err.Error(), "invalid BaseURL") {
				t.Fatalf("Mount error = %v, want invalid BaseURL", err)
			}
		})
	}
}

func TestMCPToolsCallBoundsUpstreamBody(t *testing.T) {
	t.Parallel()
	const responseLimit = 1 << 20
	cases := []struct {
		name      string
		size      int
		wantError bool
	}{
		{name: "at limit", size: responseLimit},
		{name: "over limit", size: responseLimit + 1, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, strings.Repeat("x", tc.size))
			}))
			defer upstream.Close()

			res, err := mcpSession(t, upstream.URL).CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "getEcho",
				Arguments: map[string]any{"id": "abc"},
			})
			if err != nil {
				t.Fatalf("call tool: %v", err)
			}
			if res.IsError != tc.wantError {
				t.Fatalf("IsError = %v, want %v", res.IsError, tc.wantError)
			}
			if tc.wantError && !resultContains(res, "response body exceeds") {
				t.Errorf("result content = %+v, want response limit error", res.Content)
			}
		})
	}
}

func TestMCPToolsCallConfinesRedirectsToBaseOrigin(t *testing.T) {
	t.Parallel()
	var crossOriginHits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		crossOriginHits.Add(1)
		_, _ = io.WriteString(w, "escaped")
	}))
	defer target.Close()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/final" {
			_, _ = io.WriteString(w, "same-origin")
			return
		}
		if req.URL.Path == "/echo/cross" {
			http.Redirect(w, req, target.URL+"/escaped", http.StatusTemporaryRedirect)
			return
		}
		http.Redirect(w, req, "/final", http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()

	cs := mcpSession(t, upstream.URL)
	sameOrigin, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "getEcho",
		Arguments: map[string]any{"id": "same"},
	})
	if err != nil || sameOrigin.IsError || !resultContains(sameOrigin, "same-origin") {
		t.Fatalf("same-origin redirect result = %+v, err = %v", sameOrigin, err)
	}

	crossOrigin, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "getEcho",
		Arguments: map[string]any{"id": "cross"},
	})
	if err != nil {
		t.Fatalf("cross-origin call: %v", err)
	}
	if !crossOrigin.IsError || !resultContains(crossOrigin, "redirect escapes BaseURL origin") {
		t.Fatalf("cross-origin result = %+v, want confined redirect error", crossOrigin)
	}
	if got := crossOriginHits.Load(); got != 0 {
		t.Fatalf("cross-origin target hits = %d, want 0", got)
	}
}

// roundTripperFunc adapts a func to http.RoundTripper.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// failingBody is an empty response body whose Read or Close fails on demand.
type failingBody struct {
	readErr, closeErr error
}

func (b failingBody) Read([]byte) (int, error) {
	if b.readErr != nil {
		return 0, b.readErr
	}
	return 0, io.EOF
}

func (b failingBody) Close() error { return b.closeErr }

// TestMCPToolsCallBodyFailures proves that a failed body read or close is
// reported as an IsError tool result — never as a JSON-RPC protocol error and
// never as a silently truncated reply.
func TestMCPToolsCallBodyFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body failingBody
		want string
	}{
		{name: "read", body: failingBody{readErr: errors.New("boom-read")}, want: "read response: boom-read"},
		{name: "close", body: failingBody{closeErr: errors.New("boom-close")}, want: "close response body: boom-close"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hc := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: tc.body}, nil
			})}
			cs := mcpSessionWith(t, "http://upstream.test", hc)
			res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "getEcho",
				Arguments: map[string]any{"id": "abc"},
			})
			if err != nil {
				t.Fatalf("call tool: unexpected protocol error: %v", err)
			}
			if !res.IsError {
				t.Errorf("IsError=false, want tool error: %+v", res.Content)
			}
			if !resultContains(res, tc.want) {
				t.Errorf("result content = %+v, want substring %q", res.Content, tc.want)
			}
		})
	}
}

func resultContains(res *mcp.CallToolResult, substr string) bool {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, substr) {
			return true
		}
	}
	return false
}
