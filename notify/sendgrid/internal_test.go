// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sendgrid

import (
	"net/http"
	"testing"
	"time"
)

func TestNewSenderBoundsInjectedHTTPClient(t *testing.T) {
	t.Parallel()
	injected := &http.Client{Transport: http.DefaultTransport}
	sender, err := NewSender(Options{APIKey: "key", From: "from@example.test", HTTPClient: injected})
	if err != nil {
		t.Fatal(err)
	}
	if sender.hc == injected || injected.Timeout != 0 || sender.hc.Timeout != 10*time.Second {
		t.Fatalf("sender/source timeout = %s/%s", sender.hc.Timeout, injected.Timeout)
	}
	if sender.hc.Transport != injected.Transport {
		t.Fatal("injected transport was not preserved")
	}
}
