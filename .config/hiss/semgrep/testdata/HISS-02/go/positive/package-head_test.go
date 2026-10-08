// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http"
	"testing"
)

// TestPackageHead sends through http.Head on the shared default transport.
func TestPackageHead(t *testing.T) {
	resp, err := http.Head("http://127.0.0.1:1/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}
