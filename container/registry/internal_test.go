// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
)

// TestNew_defaults asserts the zero-value Options plus nil keychain/transport
// still produce a fully usable Client (authn.DefaultKeychain,
// http.DefaultTransport, DefaultTimeout, defaultUserAgent).
func TestNew_defaults(t *testing.T) {
	t.Parallel()
	c := New(Options{}, nil, nil)
	if c.keychain != authn.DefaultKeychain {
		t.Errorf("keychain = %v, want authn.DefaultKeychain", c.keychain)
	}
	if c.transport != http.DefaultTransport {
		t.Errorf("transport = %v, want http.DefaultTransport", c.transport)
	}
	if c.userAgent != defaultUserAgent {
		t.Errorf("userAgent = %q, want %q", c.userAgent, defaultUserAgent)
	}
	if c.timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", c.timeout, DefaultTimeout)
	}
}

// TestNew_explicitValues asserts every explicit Options/keychain/transport
// value is used as-is (the boundary opposite of TestNew_defaults).
func TestNew_explicitValues(t *testing.T) {
	t.Parallel()
	kc := authn.NewMultiKeychain()
	rt := http.DefaultTransport
	c := New(Options{UserAgent: "custom/1.0", Timeout: 3 * time.Second}, kc, rt)
	if c.keychain != kc {
		t.Errorf("keychain not propagated")
	}
	if c.transport != rt {
		t.Errorf("transport not propagated")
	}
	if c.userAgent != "custom/1.0" {
		t.Errorf("userAgent = %q, want %q", c.userAgent, "custom/1.0")
	}
	if c.timeout != 3*time.Second {
		t.Errorf("timeout = %v, want %v", c.timeout, 3*time.Second)
	}
}

// TestClient_bound asserts bound() derives a context with a deadline
// (HISS-02: explicit timeout on all I/O) and that the returned cancel func
// is safe to call.
func TestClient_bound(t *testing.T) {
	t.Parallel()
	c := New(Options{Timeout: time.Minute}, authn.NewMultiKeychain(), http.DefaultTransport)
	ctx, cancel := c.bound(t.Context())
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("bound: expected a deadline on the derived context")
	}
}
