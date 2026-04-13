package apidocs_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

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

func TestMCPInitialize(t *testing.T) {
	t.Parallel()
	r := mount(t, "http://example.test")

	req := rpcReq("1", "initialize", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp struct {
		Result struct {
			ProtocolVersion string            `json:"protocolVersion"`
			ServerInfo      map[string]string `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Result.ProtocolVersion == "" {
		t.Error("empty protocolVersion")
	}
	if resp.Result.ServerInfo["name"] != "example" {
		t.Errorf("server name = %q", resp.Result.ServerInfo["name"])
	}
}

func TestMCPToolsList(t *testing.T) {
	t.Parallel()
	r := mount(t, "http://example.test")

	req := rpcReq("2", "tools/list", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range resp.Result.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"getEcho", "createThing"} {
		if !names[want] {
			t.Errorf("tools/list missing %q; got %v", want, resp.Result.Tools)
		}
	}
}

// TestMCPToolsCallProxiesRequest proves tools/call builds the right outbound
// request (path substitution, method, body) and returns the upstream reply.
func TestMCPToolsCallProxiesRequest(t *testing.T) {
	t.Parallel()
	var hitPath atomic.Value
	hitPath.Store("")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hitPath.Store(req.URL.Path)
		_, _ = io.WriteString(w, `{"echoed":true}`)
	}))
	defer upstream.Close()

	r := mount(t, upstream.URL)

	params, _ := json.Marshal(map[string]any{
		"name":      "getEcho",
		"arguments": map[string]any{"id": "abc123"},
	})
	req := rpcReq("3", "tools/call", params)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%q", err, rr.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("rpc error: %+v", resp.Error)
	}
	if resp.Result.IsError {
		t.Errorf("IsError=true: %+v", resp.Result)
	}
	if p := hitPath.Load().(string); p != "/echo/abc123" {
		t.Errorf("upstream hit %q, want /echo/abc123", p)
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, "echoed") {
		t.Errorf("result content = %+v", resp.Result.Content)
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

	r := mount(t, upstream.URL)

	params, _ := json.Marshal(map[string]any{
		"name": "createThing",
		"arguments": map[string]any{
			"body": map[string]any{"name": "widget"},
		},
	})
	req := rpcReq("4", "tools/call", params)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if body := gotBody.Load().(string); !strings.Contains(body, `"widget"`) {
		t.Errorf("upstream body = %q", body)
	}
}

func rpcReq(id, method string, params json.RawMessage) *http.Request {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	b, _ := json.Marshal(payload)
	return httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
}
