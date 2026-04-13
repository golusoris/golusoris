// Command golusoris-mcp is a standalone MCP server that exposes golusoris
// framework capabilities as MCP tools. It allows AI agents (Claude, Cursor,
// Copilot, etc.) to scaffold apps, add modules, and query framework docs.
//
// Usage:
//
//	golusoris-mcp                  # listens on :8899 by default
//	golusoris-mcp --addr :9000     # custom address
//
// The server exposes the JSON-RPC 2.0 MCP protocol at POST /mcp and serves
// the tool list at GET /mcp (method: tools/list).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/golusoris/golusoris/clikit"
)

func main() {
	var addr string

	root := clikit.New("golusoris-mcp", "MCP server for the golusoris framework")
	root.Cobra().PersistentFlags().StringVar(&addr, "addr", ":8899", "Listen address")
	root.Cobra().RunE = func(_ *cobra.Command, _ []string) error {
		return runServer(addr)
	}
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func runServer(addr string) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", mcpHandler(logger))

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		logger.Info("golusoris-mcp listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "err", err)
		}
	}()

	<-ctx.Done()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	return srv.Shutdown(shutCtx) //nolint:wrapcheck // stdlib error is descriptive
}

// mcpTools is the static list of tools exposed by the MCP server.
var mcpTools = []map[string]any{
	{
		"name":        "golusoris_init",
		"description": "Scaffold a new golusoris application with a go.mod and main.go.",
		"inputSchema": map[string]any{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"name":   map[string]any{"type": "string", "description": "App name (directory name)"},
				"module": map[string]any{"type": "string", "description": "Go module path (default: github.com/example/<name>)"},
			},
		},
	},
	{
		"name":        "golusoris_add",
		"description": "Show how to add a golusoris module (db, http, otel, cache, jobs, auth-oidc, authz, k8s) to an existing app.",
		"inputSchema": map[string]any{
			"type":     "object",
			"required": []string{"module"},
			"properties": map[string]any{
				"module": map[string]any{"type": "string", "description": "Module short name (e.g. 'db', 'http', 'jobs')"},
			},
		},
	},
	{
		"name":        "golusoris_bump",
		"description": "Show how to bump golusoris to a specific version in a downstream app.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"version": map[string]any{"type": "string", "description": "Target version (e.g. 'v0.5.0' or 'latest')"},
			},
		},
	},
}

// mcpHandler handles JSON-RPC 2.0 MCP requests.
func mcpHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{
				"jsonrpc": "2.0",
				"result":  map[string]any{"tools": mcpTools},
				"id":      nil,
			})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      any             `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusOK, jsonrpcError(nil, -32700, "Parse error"))
			return
		}

		logger.Info("mcp request", "method", req.Method)

		switch req.Method {
		case "initialize":
			writeJSON(w, http.StatusOK, map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"serverInfo":      map[string]any{"name": "golusoris-mcp", "version": "0.1.0"},
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			})

		case "tools/list":
			writeJSON(w, http.StatusOK, map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"tools": mcpTools},
			})

		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				writeJSON(w, http.StatusOK, jsonrpcError(req.ID, -32602, "Invalid params"))
				return
			}
			result := dispatchTool(p.Name, p.Arguments)
			writeJSON(w, http.StatusOK, map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"content": []map[string]any{{"type": "text", "text": result}}},
			})

		default:
			writeJSON(w, http.StatusOK, jsonrpcError(req.ID, -32601, "Method not found"))
		}
	}
}

func dispatchTool(name string, args map[string]any) string {
	switch name {
	case "golusoris_init":
		appName, _ := args["name"].(string)
		mod, _ := args["module"].(string)
		if mod == "" {
			mod = "github.com/example/" + appName
		}
		return fmt.Sprintf("Run:\n  golusoris init %s --module %s", appName, mod)

	case "golusoris_add":
		module, _ := args["module"].(string)
		return fmt.Sprintf("Run:\n  golusoris add %s", module)

	case "golusoris_bump":
		version, _ := args["version"].(string)
		if version == "" {
			version = "latest"
		}
		if version != "latest" && !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		return fmt.Sprintf("Run:\n  golusoris bump %s\n\nCheck docs/migrations/ for breaking-change notes.", version)

	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonrpcError(id any, code int, msg string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": msg},
	}
}
