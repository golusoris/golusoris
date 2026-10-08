// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPWithoutHTTPClient leaves HTTPClient nil, so the SDK falls back to
// http.DefaultClient for the whole session.
func TestMCPWithoutHTTPClient(t *testing.T) {
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	_, _ = client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/mcp"}, nil)
}
