// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http"
	"net/url"
	"testing"
)

// TestPackagePostForm sends through http.PostForm on the shared default
// transport.
func TestPackagePostForm(t *testing.T) {
	resp, err := http.PostForm("http://127.0.0.1:1/", url.Values{"k": {"v"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}
