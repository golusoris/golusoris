// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package teams

import (
	"net/http"
	"testing"
	"time"

	"github.com/golusoris/golusoris/testutil/httpclient"
)

func TestNewSenderBoundsInjectedHTTPClient(t *testing.T) {
	t.Parallel()
	injected := &http.Client{Transport: http.DefaultTransport}
	sender, err := NewSender(Options{WebhookURL: "https://example.test/hook", HTTPClient: injected})
	if err != nil {
		t.Fatal(err)
	}
	httpclient.AssertBoundedClone(t, injected, sender.hc, 10*time.Second)
}
