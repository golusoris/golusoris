// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http"
	"testing"
)

// TestPackageGet sends through http.Get, which uses http.DefaultClient and so
// the shared default transport a parallel httptest.Server.Close resets.
func TestPackageGet(t *testing.T) {
	resp, err := http.Get("http://127.0.0.1:1/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}
