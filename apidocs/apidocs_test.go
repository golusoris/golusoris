package apidocs_test

import (
	"context"
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
	r := chi.NewRouter()
	if err := apidocs.Mount(r, apidocs.Options{
		Title:         "Example",
		Spec:          []byte(minimalSpec),
		BaseURL:       baseURL,
		ServerName:    "example",
		ServerVersion: "0.0.1",
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
	srv := httptest.NewServer(mount(t, baseURL))
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

func resultContains(res *mcp.CallToolResult, substr string) bool {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, substr) {
			return true
		}
	}
	return false
}
