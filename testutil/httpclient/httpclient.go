// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package httpclient provides test assertions for HTTP clients and transports.
package httpclient

import (
	"net/http"
	"testing"
	"time"
)

// AssertBoundedClone asserts that clone is a distinct bounded client with timeout and preserved transport.
func AssertBoundedClone(tb testing.TB, source, clone *http.Client, timeout time.Duration) {
	tb.Helper()
	if clone == source || source.Timeout != 0 || clone.Timeout != timeout {
		tb.Fatalf("client clone/source timeout = %s/%s", clone.Timeout, source.Timeout)
	}
	if clone.Transport != source.Transport {
		tb.Fatal("injected transport was not preserved")
	}
}

// RoundTripError returns the error of a round trip that must fail; a returned response fails tb after its body is closed.
func RoundTripError(tb testing.TB, rt http.RoundTripper, req *http.Request) error {
	tb.Helper()
	resp, err := rt.RoundTrip(req)
	if resp == nil {
		return err
	}
	var closeErr error
	if resp.Body != nil {
		closeErr = resp.Body.Close()
	}
	tb.Fatalf("unexpected response %d (close: %v)", resp.StatusCode, closeErr)
	return err
}
