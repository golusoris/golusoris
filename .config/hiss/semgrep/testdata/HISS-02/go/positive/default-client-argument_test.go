// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http"
	"testing"
)

func fetch(c *http.Client, target string) error {
	resp, err := c.Get(target)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// TestDefaultClientArgument hands http.DefaultClient to the code under test,
// which then sends on the shared default transport.
func TestDefaultClientArgument(t *testing.T) {
	if err := fetch(http.DefaultClient, "http://127.0.0.1:1/"); err == nil {
		t.Fatal("expected a dial error")
	}
}
