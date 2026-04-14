package apidocs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
)

// toolPathRE bounds the set of characters that can appear in a tool-call
// path. buildCall url.PathEscape's every user-supplied arg, so any char
// outside this set is a bug upstream — fail closed rather than forward
// the request. This is the static sanitizer CodeQL's request-forgery
// query recognizes for the s.opts.HTTPClient.Do(...) sink below.
var toolPathRE = regexp.MustCompile(`^/[A-Za-z0-9/_.~\-%?&=+]*$`)

// MCP JSON-RPC 2.0 surface. Implements the server side of the "streamable
// HTTP" transport in its stateless form: each POST is an independent
// request/response, no SSE. Covers the minimum methods an MCP client needs
// to discover + invoke tools: initialize, tools/list, tools/call.
//
// Spec: https://modelcontextprotocol.io/specification — this is a pragmatic
// subset; apps needing the full stateful transport (resources, prompts,
// sampling, subscriptions) should layer their own MCP SDK.

const (
	jsonRPCVersion     = "2.0"
	mcpProtocolVersion = "2025-03-26"
	methodInitialize   = "initialize"
	methodToolsList    = "tools/list"
	methodToolsCall    = "tools/call"
)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Well-known JSON-RPC 2.0 error codes.
const (
	errParseError     = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternalError  = -32603
)

type initializeResult struct {
	ProtocolVersion string            `json:"protocolVersion"`
	ServerInfo      map[string]string `json:"serverInfo"`
	Capabilities    map[string]any    `json:"capabilities"`
}

type toolsListResult struct {
	Tools []Tool `json:"tools"`
}

// Tool is an MCP tool definition (exported so tests + callers can inspect).
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
	// Internal fields (not serialized over MCP) describing how to invoke
	// the tool via HTTP. Populated by mcpTools.
	method string
	path   string
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type toolsCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// mcpServer holds the precomputed tool catalog + bits needed to dispatch
// tools/call to the real API.
type mcpServer struct {
	opts   Options
	tools  []Tool
	byName map[string]*Tool
}

func newMCPHandler(opts Options) (http.HandlerFunc, error) {
	tools, err := openAPIToTools(opts.Spec)
	if err != nil {
		return nil, fmt.Errorf("apidocs/mcp: build tools: %w", err)
	}
	byName := make(map[string]*Tool, len(tools))
	for i := range tools {
		byName[tools[i].Name] = &tools[i]
	}
	srv := &mcpServer{opts: opts, tools: tools, byName: byName}
	return srv.serve, nil
}

func (s *mcpServer) serve(w http.ResponseWriter, r *http.Request) {
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, errParseError, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != jsonRPCVersion {
		writeRPCError(w, req.ID, errInvalidRequest, "jsonrpc must be 2.0")
		return
	}
	switch req.Method {
	case methodInitialize:
		s.handleInitialize(w, req)
	case methodToolsList:
		s.handleToolsList(w, req)
	case methodToolsCall:
		s.handleToolsCall(r, w, req)
	default:
		writeRPCError(w, req.ID, errMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *mcpServer) handleInitialize(w http.ResponseWriter, req jsonRPCRequest) {
	writeRPCResult(w, req.ID, initializeResult{
		ProtocolVersion: mcpProtocolVersion,
		ServerInfo: map[string]string{
			"name":    s.opts.ServerName,
			"version": s.opts.ServerVersion,
		},
		Capabilities: map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
	})
}

func (s *mcpServer) handleToolsList(w http.ResponseWriter, req jsonRPCRequest) {
	writeRPCResult(w, req.ID, toolsListResult{Tools: s.tools})
}

func (s *mcpServer) handleToolsCall(r *http.Request, w http.ResponseWriter, req jsonRPCRequest) {
	var p toolsCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, errInvalidParams, "bad params: "+err.Error())
		return
	}
	tool, ok := s.byName[p.Name]
	if !ok {
		writeRPCError(w, req.ID, errInvalidParams, "unknown tool: "+p.Name)
		return
	}
	if s.opts.BaseURL == "" {
		writeRPCError(w, req.ID, errInternalError, "apidocs: BaseURL is unset; tool calls disabled")
		return
	}

	path, body, ct, err := buildCall(tool, p.Arguments)
	if err != nil {
		writeRPCError(w, req.ID, errInvalidParams, err.Error())
		return
	}
	// Two layers of sanitization on the constructed path+URL:
	//   1) toolPathRE rejects anything outside a tight URL-safe charset —
	//      user args are url.PathEscape'd in buildCall so malformed input
	//      indicates a bug, not a benign edge case.
	//   2) safeResolveURL pins the result's scheme+host to s.opts.BaseURL.
	if !toolPathRE.MatchString(path) {
		writeRPCError(w, req.ID, errInvalidParams, "apidocs: tool path contains disallowed characters")
		return
	}
	callURL, err := safeResolveURL(s.opts.BaseURL, path)
	if err != nil {
		writeRPCError(w, req.ID, errInvalidParams, err.Error())
		return
	}
	httpReq, err := http.NewRequestWithContext(r.Context(), tool.method, callURL, body)
	if err != nil {
		writeRPCError(w, req.ID, errInternalError, "build request: "+err.Error())
		return
	}
	if ct != "" {
		httpReq.Header.Set("Content-Type", ct)
	}
	// Forward request-ID if the outer middleware set one on the MCP request.
	if rid := r.Header.Get("X-Request-ID"); rid != "" {
		httpReq.Header.Set("X-Request-ID", rid)
	}

	resp, err := s.opts.HTTPClient.Do(httpReq)
	if err != nil {
		writeRPCResult(w, req.ID, toolsCallResult{
			Content: []toolContent{{Type: "text", Text: "request failed: " + err.Error()}},
			IsError: true,
		})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, _ := io.ReadAll(resp.Body)
	result := toolsCallResult{
		Content: []toolContent{{
			Type: "text",
			Text: fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, string(bodyBytes)),
		}},
		IsError: resp.StatusCode >= 400,
	}
	writeRPCResult(w, req.ID, result)
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  result,
	})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: msg},
	})
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
