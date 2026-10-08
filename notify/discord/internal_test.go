// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package discord

import (
	"net/http"
	"testing"
	"time"
)

func TestNewSenderClonesAndBoundsHTTPClient(t *testing.T) {
	t.Parallel()

	transport := http.DefaultTransport
	injected := &http.Client{Transport: transport}
	sender, err := NewSender(Options{WebhookURL: "https://example.test/hook", HTTPClient: injected})
	if err != nil {
		t.Fatal(err)
	}
	if sender.hc == injected {
		t.Fatal("NewSender retained caller-owned client")
	}
	if sender.hc.Timeout != 10*time.Second || injected.Timeout != 0 {
		t.Fatalf("sender/source timeout = %s/%s, want 10s/0s", sender.hc.Timeout, injected.Timeout)
	}
	if sender.hc.Transport != transport {
		t.Fatal("NewSender replaced injected transport")
	}

	const customTimeout = 23 * time.Second
	custom, err := NewSender(Options{
		WebhookURL: "https://example.test/hook",
		HTTPClient: &http.Client{Timeout: customTimeout},
	})
	if err != nil {
		t.Fatal(err)
	}
	if custom.hc.Timeout != customTimeout {
		t.Fatalf("custom timeout = %s, want %s", custom.hc.Timeout, customTimeout)
	}

	negative, err := NewSender(Options{
		WebhookURL: "https://example.test/hook",
		HTTPClient: &http.Client{Timeout: -time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if negative.hc.Timeout != 10*time.Second {
		t.Fatalf("negative timeout = %s, want 10s", negative.hc.Timeout)
	}

	defaults, err := NewSender(Options{WebhookURL: "https://example.test/hook"})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.hc.Timeout != 10*time.Second {
		t.Fatalf("default timeout = %s, want 10s", defaults.hc.Timeout)
	}
}
