// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apidocs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSanitizePath(t *testing.T) {
	t.Parallel()
	got := sanitizePath("/users/{id}/posts")
	want := "_users_id_posts"
	if got != want {
		t.Errorf("sanitizePath = %q, want %q", got, want)
	}
}

// TestBuildProxyRequest_success proves a well-formed tool + args produce
// the expected outbound *http.Request (method, URL, and a JSON body with
// its Content-Type header set).
func TestBuildProxyRequest_success(t *testing.T) {
	t.Parallel()
	tool := Tool{
		method:          http.MethodPost,
		path:            "/things/{id}",
		params:          []toolParameter{{name: "id", location: openapi3.ParameterInPath, style: openapi3.SerializationSimple}},
		bodyContentType: "application/json",
	}
	args := json.RawMessage(`{"id":"abc","body":{"name":"widget"}}`)
	req, errRes := buildProxyRequest(context.Background(), Options{BaseURL: "http://api.test"}, tool, args)
	if errRes != nil {
		t.Fatalf("unexpected error result: %+v", errRes.Content)
	}
	if req.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", req.Method)
	}
	if req.URL.String() != "http://api.test/things/abc" {
		t.Errorf("URL = %q, want http://api.test/things/abc", req.URL.String())
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// TestBuildProxyRequest_buildCallError proves malformed argument JSON is
// reported as an IsError tool result, not a nil-pointer or panic.
func TestBuildProxyRequest_buildCallError(t *testing.T) {
	t.Parallel()
	tool := Tool{method: http.MethodGet, path: "/echo"}
	req, errRes := buildProxyRequest(context.Background(), Options{BaseURL: "http://api.test"}, tool, json.RawMessage(`not-json`))
	if req != nil {
		t.Fatalf("expected nil request, got %+v", req)
	}
	if errRes == nil || !errRes.IsError {
		t.Fatal("expected an IsError tool result")
	}
	if !resultContainsText(errRes, "unmarshal arguments") {
		t.Errorf("result = %+v, want message about unmarshal arguments", errRes.Content)
	}
}

// TestBuildProxyRequest_disallowedPathCharacters is the boundary case where
// tool.path itself (not user input — buildCall escapes that) contains a
// character outside toolPathRE's charset, which fails closed rather than
// forwarding the request.
func TestBuildProxyRequest_disallowedPathCharacters(t *testing.T) {
	t.Parallel()
	tool := Tool{method: http.MethodGet, path: "/ba d path"}
	req, errRes := buildProxyRequest(context.Background(), Options{BaseURL: "http://api.test"}, tool, nil)
	if req != nil {
		t.Fatalf("expected nil request, got %+v", req)
	}
	if !resultContainsText(errRes, "disallowed characters") {
		t.Errorf("result = %+v, want disallowed-characters message", errRes.Content)
	}
}

// TestBuildProxyRequest_safeResolveURLError proves an invalid BaseURL is
// surfaced as an IsError tool result via safeResolveURL.
func TestBuildProxyRequest_safeResolveURLError(t *testing.T) {
	t.Parallel()
	tool := Tool{method: http.MethodGet, path: "/echo"}
	req, errRes := buildProxyRequest(context.Background(), Options{BaseURL: "not-a-url"}, tool, nil)
	if req != nil {
		t.Fatalf("expected nil request, got %+v", req)
	}
	if !resultContainsText(errRes, "invalid BaseURL") {
		t.Errorf("result = %+v, want invalid-BaseURL message", errRes.Content)
	}
}

// TestBuildProxyRequest_newRequestError proves an invalid HTTP method
// (rejected by http.NewRequestWithContext) is surfaced as an IsError tool
// result rather than propagated as a Go error.
func TestBuildProxyRequest_newRequestError(t *testing.T) {
	t.Parallel()
	tool := Tool{method: "BAD METHOD", path: "/echo"}
	req, errRes := buildProxyRequest(context.Background(), Options{BaseURL: "http://api.test"}, tool, nil)
	if req != nil {
		t.Fatalf("expected nil request, got %+v", req)
	}
	if !resultContainsText(errRes, "build request") {
		t.Errorf("result = %+v, want build-request message", errRes.Content)
	}
}

func resultContainsText(res *mcp.CallToolResult, substr string) bool {
	if res == nil {
		return false
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok && strings.Contains(tc.Text, substr) {
			return true
		}
	}
	return false
}

func TestSafeResolveURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		base    string
		path    string
		want    string
		wantErr bool
	}{
		{"simple join", "http://api.test", "/echo/abc", "http://api.test/echo/abc", false},
		{"query preserved", "https://api.test", "/x?q=1", "https://api.test/x?q=1", false},
		{"base path preserved", "https://api.test/gateway/v1", "/x?q=1", "https://api.test/gateway/v1/x?q=1", false},
		{"scheme-relative escapes origin", "http://api.test", "//evil.test/x", "", true},
		{"non-http scheme", "ftp://api.test", "/x", "", true},
		{"userinfo", "https://user:secret@api.test", "/x", "", true},
		{"empty hostname", "http://:8080", "/x", "", true},
		{"invalid base", "not-a-url", "/x", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := safeResolveURL(tt.base, tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("safeResolveURL err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("safeResolveURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSameOriginClientClonesAndComposesRedirectPolicy(t *testing.T) {
	t.Parallel()
	callerErr := errors.New("caller stopped redirect")
	callerChecks := 0
	original := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			callerChecks++
			return callerErr
		},
	}
	confined, err := sameOriginClient(original, "http://api.test")
	if err != nil {
		t.Fatalf("sameOriginClient: %v", err)
	}
	if confined == original {
		t.Fatal("sameOriginClient returned caller-owned client")
	}
	if original.Timeout != 0 || confined.Timeout != defaultMCPRequestTimeout {
		t.Fatalf("original/confined timeout = %s/%s", original.Timeout, confined.Timeout)
	}

	const customTimeout = 23 * time.Second
	custom, err := sameOriginClient(&http.Client{Timeout: customTimeout}, "https://api.test")
	if err != nil {
		t.Fatalf("sameOriginClient custom timeout: %v", err)
	}
	if custom.Timeout != customTimeout {
		t.Fatalf("custom timeout = %s, want %s", custom.Timeout, customTimeout)
	}

	via, err := http.NewRequest(http.MethodGet, "http://api.test/start", nil)
	if err != nil {
		t.Fatalf("build via request: %v", err)
	}
	crossOrigin, err := http.NewRequest(http.MethodGet, "https://api.test/next", nil)
	if err != nil {
		t.Fatalf("build cross-origin request: %v", err)
	}
	if got := confined.CheckRedirect(crossOrigin, []*http.Request{via}); !errors.Is(got, errRedirectEscapesBaseOrigin) {
		t.Fatalf("cross-origin redirect error = %v", got)
	}
	if callerChecks != 0 {
		t.Fatalf("caller redirect checks = %d, want 0 for rejected origin", callerChecks)
	}

	sameOrigin, err := http.NewRequest(http.MethodGet, "http://API.TEST:80/next", nil)
	if err != nil {
		t.Fatalf("build same-origin request: %v", err)
	}
	if got := confined.CheckRedirect(sameOrigin, []*http.Request{via}); !errors.Is(got, callerErr) {
		t.Fatalf("same-origin redirect error = %v, want caller policy", got)
	}
	if callerChecks != 1 {
		t.Fatalf("caller redirect checks = %d, want 1", callerChecks)
	}

	redirectChain := make([]*http.Request, 10)
	if got := confined.CheckRedirect(sameOrigin, redirectChain); got == nil || !strings.Contains(got.Error(), "10 redirects") {
		t.Fatalf("redirect-chain error = %v, want hard limit", got)
	}
	if callerChecks != 1 {
		t.Fatalf("caller redirect checks = %d, want hard limit before caller policy", callerChecks)
	}
}

const parameterSpec = `openapi: 3.0.3
info:
  title: Parameters
  version: "1"
paths:
  /items/{id}:
    parameters:
      - name: id
        in: path
        required: true
        schema:
          type: string
      - name: X-Tenant
        in: header
        required: true
        schema:
          type: string
      - name: session
        in: cookie
        schema:
          type: string
      - name: mode
        in: query
        schema:
          type: string
    get:
      operationId: getItem
      parameters:
        - name: mode
          in: query
          required: true
          description: operation override
          schema:
            type: string
      responses:
        '200':
          description: ok
`

func TestPathItemParametersMergeAndPreserveLocations(t *testing.T) {
	t.Parallel()
	tools, err := openAPIToTools([]byte(parameterSpec))
	if err != nil {
		t.Fatalf("openAPIToTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(tools))
	}

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if schemaErr := json.Unmarshal(tools[0].InputSchema, &schema); schemaErr != nil {
		t.Fatalf("unmarshal input schema: %v", schemaErr)
	}
	for _, name := range []string{"id", "X-Tenant", "session", "mode"} {
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("input schema missing %q", name)
		}
	}
	for _, name := range []string{"id", "X-Tenant", "mode"} {
		if !containsString(schema.Required, name) {
			t.Errorf("required = %v, missing %q", schema.Required, name)
		}
	}
	if got := string(schema.Properties["mode"]); !strings.Contains(got, "operation override") {
		t.Errorf("mode schema = %s, want operation-level override", got)
	}

	req, errRes := buildProxyRequest(
		context.Background(),
		Options{BaseURL: "https://api.test"},
		tools[0],
		json.RawMessage(`{"id":"a/b","X-Tenant":"acme","session":"token","mode":"full"}`),
	)
	if errRes != nil {
		if content, ok := errRes.Content[0].(*mcp.TextContent); ok {
			t.Fatalf("buildProxyRequest: %s", content.Text)
		}
		t.Fatalf("buildProxyRequest: %+v", errRes.Content)
	}
	if got := req.URL.String(); got != "https://api.test/items/a%2Fb?mode=full" {
		t.Errorf("URL = %q, want path and query parameters only", got)
	}
	if got := req.Header.Get("X-Tenant"); got != "acme" {
		t.Errorf("X-Tenant = %q, want acme", got)
	}
	cookie, err := req.Cookie("session")
	if err != nil || cookie.Value != "token" {
		t.Errorf("session cookie = %+v, err = %v", cookie, err)
	}
}

func TestBuildProxyRequestRejectsInvalidCookieValue(t *testing.T) {
	t.Parallel()
	tools, err := openAPIToTools([]byte(parameterSpec))
	if err != nil {
		t.Fatalf("openAPIToTools: %v", err)
	}
	req, errRes := buildProxyRequest(
		context.Background(),
		Options{BaseURL: "https://api.test"},
		tools[0],
		json.RawMessage("{\"id\":\"item\",\"X-Tenant\":\"acme\",\"session\":\"bad\\nvalue\",\"mode\":\"full\"}"),
	)
	if req != nil || !resultContainsText(errRes, "invalid request cookie") {
		t.Fatalf("request = %+v, result = %+v, want invalid-cookie rejection", req, errRes)
	}
}

func TestBuildProxyRequestRejectsPathDotSegments(t *testing.T) {
	t.Parallel()
	tools, err := openAPIToTools([]byte(parameterSpec))
	if err != nil {
		t.Fatalf("openAPIToTools: %v", err)
	}
	for _, value := range []string{".", ".."} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			args, marshalErr := json.Marshal(map[string]any{"id": value})
			if marshalErr != nil {
				t.Fatalf("marshal arguments: %v", marshalErr)
			}
			req, errRes := buildProxyRequest(
				context.Background(),
				Options{BaseURL: "https://api.test"},
				tools[0],
				args,
			)
			if req != nil || !resultContainsText(errRes, "dot segment") {
				t.Fatalf("request = %+v, result = %+v, want dot-segment rejection", req, errRes)
			}
		})
	}

	req, errRes := buildProxyRequest(
		context.Background(),
		Options{BaseURL: "https://api.test"},
		tools[0],
		json.RawMessage(`{"id":".hidden"}`),
	)
	if errRes != nil {
		t.Fatalf("safe dot-prefixed segment: %+v", errRes.Content)
	}
	if got := req.URL.String(); got != "https://api.test/items/.hidden" {
		t.Fatalf("safe URL = %q, want declared /items/{id} shape", got)
	}
}

func TestBuildProxyRequestSerializesOpenAPIParameterStyles(t *testing.T) {
	t.Parallel()
	spec := []byte(`openapi: 3.0.3
info:
  title: Serialization
  version: "1"
paths:
  /items/{simple}/{label}/{matrix}:
    get:
      operationId: serialize
      parameters:
        - {name: simple, in: path, required: true, style: simple, explode: false, schema: {type: array, items: {type: string}}}
        - {name: label, in: path, required: true, style: label, explode: true, schema: {type: array, items: {type: string}}}
        - name: matrix
          in: path
          required: true
          style: matrix
          explode: true
          schema:
            type: object
            additionalProperties: {type: string}
        - {name: tags, in: query, schema: {type: array, items: {type: string}}}
        - name: compact
          in: query
          style: form
          explode: false
          schema: {type: object, additionalProperties: {type: string}}
        - name: expanded
          in: query
          style: form
          explode: true
          schema: {type: object, additionalProperties: {type: string}}
        - {name: spaces, in: query, style: spaceDelimited, explode: false, schema: {type: array, items: {type: string}}}
        - {name: pipes, in: query, style: pipeDelimited, explode: false, schema: {type: array, items: {type: string}}}
        - name: deep
          in: query
          style: deepObject
          explode: true
          schema: {type: object, additionalProperties: {type: string}}
        - name: X-Filter
          in: header
          style: simple
          explode: true
          schema: {type: object, additionalProperties: {type: string}}
        - name: preferences
          in: cookie
          style: form
          explode: true
          schema: {type: object, additionalProperties: {type: string}}
      responses: {'200': {description: ok}}
`)
	tools, err := openAPIToTools(spec)
	if err != nil {
		t.Fatalf("openAPIToTools: %v", err)
	}
	filter := map[string]string{"role": "admin", "color": "blue"}
	args, err := json.Marshal(map[string]any{
		"simple":      []string{"red", "blue"},
		"label":       []string{"red", "blue"},
		"matrix":      filter,
		"tags":        []string{"red", "blue"},
		"compact":     filter,
		"expanded":    filter,
		"spaces":      []string{"red", "blue"},
		"pipes":       []string{"red", "blue"},
		"deep":        filter,
		"X-Filter":    filter,
		"preferences": filter,
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	req, errRes := buildProxyRequest(
		context.Background(), Options{BaseURL: "https://api.test"}, tools[0], args,
	)
	if errRes != nil {
		if content, ok := errRes.Content[0].(*mcp.TextContent); ok {
			t.Fatalf("buildProxyRequest: %s", content.Text)
		}
		t.Fatalf("buildProxyRequest: %+v", errRes.Content)
	}
	if got, want := req.URL.EscapedPath(), "/items/red,blue/.red.blue/;color=blue;role=admin"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	wantQuery := "color=blue&compact=color%2Cblue%2Crole%2Cadmin&deep%5Bcolor%5D=blue&deep%5Brole%5D=admin&pipes=red%7Cblue&role=admin&spaces=red+blue&tags=red&tags=blue"
	if got := req.URL.RawQuery; got != wantQuery {
		t.Errorf("query = %q, want %q", got, wantQuery)
	}
	if got := req.Header.Get("X-Filter"); got != "color=blue,role=admin" {
		t.Errorf("X-Filter = %q, want exploded object", got)
	}
	for name, want := range filter {
		cookie, cookieErr := req.Cookie(name)
		if cookieErr != nil || cookie.Value != want {
			t.Errorf("cookie %q = %+v, err = %v", name, cookie, cookieErr)
		}
	}
}

func TestOpenAPIToToolsSortsByName(t *testing.T) {
	t.Parallel()
	spec := []byte(`openapi: 3.0.3
info:
  title: Order
  version: "1"
paths:
  /z:
    get:
      operationId: middle
      responses: {'200': {description: ok}}
  /a:
    post:
      operationId: zebra
      responses: {'200': {description: ok}}
    get:
      operationId: alpha
      responses: {'200': {description: ok}}
`)
	for run := range 32 {
		tools, err := openAPIToTools(spec)
		if err != nil {
			t.Fatalf("openAPIToTools run %d: %v", run, err)
		}
		got := make([]string, len(tools))
		for i := range tools {
			got[i] = tools[i].Name
		}
		want := []string{"alpha", "middle", "zebra"}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("tool names run %d = %v, want %v", run, got, want)
			}
		}
	}
}

func TestOpenAPIToToolsRejectsDuplicateDerivedNames(t *testing.T) {
	t.Parallel()
	spec := []byte(`openapi: 3.0.3
info:
  title: Name collision
  version: "1"
paths:
  /users/{id}:
    get:
      parameters:
        - {name: id, in: path, required: true, schema: {type: string}}
      responses: {'200': {description: ok}}
  /users/id:
    get:
      responses: {'200': {description: ok}}
`)
	_, err := openAPIToTools(spec)
	if err == nil {
		t.Fatal("openAPIToTools error = nil, want duplicate tool-name rejection")
	}
	for _, want := range []string{`get_users_id`, `GET /users/{id}`, `GET /users/id`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("openAPIToTools error = %q, missing %q", err, want)
		}
	}
}

func TestBuildProxyRequestPreservesJSONRequestMediaType(t *testing.T) {
	t.Parallel()
	for _, contentType := range []string{"application/merge-patch+json", "application/vnd.acme.widget+json"} {
		t.Run(contentType, func(t *testing.T) {
			t.Parallel()
			spec := []byte(`openapi: 3.0.3
info:
  title: Vendor JSON
  version: "1"
paths:
  /things:
    patch:
      requestBody:
        required: true
        content:
          ` + contentType + `:
            schema: {type: object}
      responses: {'200': {description: ok}}
`)
			tools, err := openAPIToTools(spec)
			if err != nil {
				t.Fatalf("openAPIToTools: %v", err)
			}
			req, errRes := buildProxyRequest(
				context.Background(),
				Options{BaseURL: "https://api.test"},
				tools[0],
				json.RawMessage(`{"body":{"name":"widget"}}`),
			)
			if errRes != nil {
				t.Fatalf("buildProxyRequest: %+v", errRes.Content)
			}
			if got := req.Header.Get("Content-Type"); got != contentType {
				t.Fatalf("Content-Type = %q, want %q", got, contentType)
			}
		})
	}
}

func TestOpenAPIToToolsRejectsReservedBodyParameter(t *testing.T) {
	t.Parallel()
	spec := []byte(`openapi: 3.0.3
info:
  title: Reserved parameter
  version: "1"
paths:
  /items:
    post:
      operationId: createItem
      parameters:
        - {name: body, in: query, schema: {type: string}}
      requestBody:
        content:
          application/json:
            schema: {type: object}
      responses:
        '200': {description: ok}
`)
	_, err := openAPIToTools(spec)
	if err == nil || !strings.Contains(err.Error(), `parameter "body" is reserved`) {
		t.Fatalf("openAPIToTools error = %v, want reserved body parameter", err)
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}
