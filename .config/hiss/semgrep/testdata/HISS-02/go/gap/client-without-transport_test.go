// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestClientWithoutTransport sends on http.DefaultTransport because a nil
// Transport means the default. The rule does not decide it: identity tests
// build the same literal and send nothing, so telling them apart needs
// dataflow from the literal to a send.
func TestClientWithoutTransport(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	resp, err := (&http.Client{Timeout: time.Second}).Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}
