// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"net/http"
	"testing"
)

type rewrite struct{ base http.RoundTripper }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.base.RoundTrip(req)
}

// TestDefaultTransportAsBase hands http.DefaultTransport to a wrapper that
// sends on it later. The rule does not decide it: the same value also appears
// in identity assertions, and following it into RoundTrip needs dataflow.
func TestDefaultTransportAsBase(t *testing.T) {
	client := &http.Client{Transport: rewrite{base: http.DefaultTransport}}
	resp, err := client.Get("http://127.0.0.1:1/")
	if err == nil {
		_ = resp.Body.Close()
	}
}
