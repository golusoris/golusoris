// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package typesense

import (
	"net/http"
	"testing"
	"time"
)

func TestNewBackendClonesAndBoundsInjectedClient(t *testing.T) {
	t.Parallel()
	injected := &http.Client{}
	backend, err := NewBackend(Options{URL: "http://example.test", APIKey: "k", HTTPClient: injected})
	if err != nil {
		t.Fatal(err)
	}
	if backend.hc == injected {
		t.Fatal("NewBackend retained caller-owned client")
	}
	if injected.Timeout != 0 || backend.hc.Timeout != 10*time.Second {
		t.Fatalf("injected/backend timeout = %v/%v", injected.Timeout, backend.hc.Timeout)
	}
}
