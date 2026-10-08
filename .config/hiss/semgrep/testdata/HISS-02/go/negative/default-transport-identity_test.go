// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http"
	"testing"
)

type wrapper struct{ transport http.RoundTripper }

// TestDefaultTransportIdentity asserts that a constructor keeps the default
// transport; comparing values sends no request.
func TestDefaultTransportIdentity(t *testing.T) {
	w := wrapper{transport: http.DefaultTransport}
	if w.transport != http.DefaultTransport {
		t.Fatal("transport replaced")
	}
}
