// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package slack

import (
	"net/http"
	"testing"
	"time"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/testutil/httpclient"
)

func TestNewSenderBoundsInjectedHTTPClient(t *testing.T) {
	t.Parallel()
	injected := &http.Client{Transport: http.DefaultTransport}
	sender, err := NewSender(Options{WebhookURL: "https://example.test/hook", HTTPClient: injected})
	if err != nil {
		t.Fatal(err)
	}
	httpclient.AssertBoundedClone(t, injected, sender.hc, notify.DefaultRequestTimeout)
}

func TestNewSenderDefaultTimeout(t *testing.T) {
	t.Parallel()

	sender, err := NewSender(Options{WebhookURL: "https://example.test/hook"})
	if err != nil {
		t.Fatal(err)
	}
	if sender.hc.Timeout != notify.DefaultRequestTimeout {
		t.Fatalf("nil HTTPClient timeout = %v, want %v", sender.hc.Timeout, notify.DefaultRequestTimeout)
	}

	const explicitTimeout = 5 * time.Second
	explicitSender, err := NewSender(Options{
		WebhookURL: "https://example.test/hook",
		HTTPClient: &http.Client{Timeout: explicitTimeout},
	})
	if err != nil {
		t.Fatal(err)
	}
	if explicitSender.hc.Timeout != explicitTimeout {
		t.Fatalf("explicit HTTPClient timeout = %v, want %v", explicitSender.hc.Timeout, explicitTimeout)
	}
}
