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

func mount(t *testing.T, baseURL string) chi.Router {
	t.Helper()
	return mountWith(t, baseURL, nil)
}

// mountWith is mount with an explicit outbound client for the /mcp proxy
// (nil keeps http.DefaultClient).
func mountWith(t *testing.T, baseURL string, hc *http.Client) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	if err := apidocs.Mount(r, apidocs.Options{
		Title:         "Example",
		Spec:          []byte(minimalSpec),
		BaseURL:       baseURL,
		ServerName:    "example",
		ServerVersion: "0.0.1",
		HTTPClient:    hc,
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
	srv := httptest.NewServer(mountWith(t, baseURL, hc))
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
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
