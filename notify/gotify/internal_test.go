// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gotify

import (
	"net/http"
	"testing"
	"time"
)

func TestNewSenderBoundsInjectedHTTPClient(t *testing.T) {
	t.Parallel()
	injected := &http.Client{Transport: http.DefaultTransport}
	sender, err := NewSender(Options{ServerURL: "https://example.test", AppToken: "token", HTTPClient: injected})
	if err != nil {
		t.Fatal(err)
	}
	assertBoundedClone(t, injected, sender.hc)
}

func assertBoundedClone(t *testing.T, source, clone *http.Client) {
	t.Helper()
	if clone == source || source.Timeout != 0 || clone.Timeout != 10*time.Second {
		t.Fatalf("client clone/source timeout = %s/%s", clone.Timeout, source.Timeout)
	}
	if clone.Transport != source.Transport {
		t.Fatal("injected transport was not preserved")
	}
}
