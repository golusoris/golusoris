// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package captcha

import (
	"net/http"
	"testing"
)

func TestNewTurnstileTypedNilClientUsesDefault(t *testing.T) {
	t.Parallel()
	var client *http.Client
	verifier := NewTurnstile("secret", client)
	httpVerifier, ok := verifier.(*httpVerifier)
	if !ok {
		t.Fatalf("NewTurnstile returned %T, want *httpVerifier", verifier)
	}
	defaultClient, ok := httpVerifier.client.(*http.Client)
	if !ok || defaultClient == nil {
		t.Fatalf("client = %T, want non-nil *http.Client", httpVerifier.client)
	}
}
