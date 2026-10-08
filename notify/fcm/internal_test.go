// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package fcm

import (
	"net/http"
	"testing"
	"time"
)

func TestNewSenderClonesAndBoundsInjectedClient(t *testing.T) {
	t.Parallel()
	injected := &http.Client{}
	sender, err := NewSender(Options{
		ServiceAccount: &ServiceAccount{
			ProjectID: "project", ClientEmail: "sender@example.test", PrivateKey: "test",
		},
		HTTPClient: injected,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sender.hc == injected {
		t.Fatal("NewSender retained caller-owned client")
	}
	if injected.Timeout != 0 || sender.hc.Timeout != defaultRequestTimeout {
		t.Fatalf("injected/sender timeout = %v/%v", injected.Timeout, sender.hc.Timeout)
	}
	if sender.maxResponseBytes != defaultMaxResponseBytes {
		t.Fatalf("max response bytes = %d, want %d", sender.maxResponseBytes, defaultMaxResponseBytes)
	}
	if sender.hc.Timeout != 10*time.Second {
		t.Fatalf("timeout = %v, want 10s", sender.hc.Timeout)
	}
}
