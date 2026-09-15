// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apidocs

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

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
	tool := Tool{method: http.MethodPost, path: "/things/{id}"}
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
		{"scheme-relative escapes origin", "http://api.test", "//evil.test/x", "", true},
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
