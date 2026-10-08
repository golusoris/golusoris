// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPWithServerClient names the server's own client for the session.
func TestMCPWithServerClient(t *testing.T) {
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: srv.Client()}
	_, _ = client.Connect(t.Context(), transport, nil)
}
