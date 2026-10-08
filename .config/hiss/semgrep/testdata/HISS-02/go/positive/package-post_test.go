// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http"
	"strings"
	"testing"
)

// TestPackagePost sends through http.Post on the shared default transport.
func TestPackagePost(t *testing.T) {
	resp, err := http.Post("http://127.0.0.1:1/", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}
