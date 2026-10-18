// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package httpclient provides test assertions for cloned and bounded HTTP clients.
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
