// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http"
	"testing"
	"time"
)

// TestPrivateTransport sends through a cloned transport that no
// httptest.Server.Close can reach.
func TestPrivateTransport(t *testing.T) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	resp, err := client.Get("http://127.0.0.1:1/")
	if err == nil {
		_ = resp.Body.Close()
	}
}
