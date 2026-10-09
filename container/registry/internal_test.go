// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
)

type typedNilKeychain struct{}

func (*typedNilKeychain) Resolve(authn.Resource) (authn.Authenticator, error) {
	return authn.Anonymous, nil
}

type typedNilTransport struct{}

func (*typedNilTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("typed-nil transport must not be called")
}

// requirePrivateTransport fails unless rt is a *http.Transport other than the
// shared http.DefaultTransport, whose idle pool any code may close (#703).
func requirePrivateTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	if _, ok := rt.(*http.Transport); !ok || rt == http.DefaultTransport {
		t.Fatalf("transport = %T shared=%v, want a private *http.Transport", rt, rt == http.DefaultTransport)
	}
}

// TestNew_defaults asserts the zero-value Options plus nil keychain/transport
// still produce a fully usable Client (authn.DefaultKeychain, a private clone
// of http.DefaultTransport, DefaultTimeout, defaultUserAgent).
func TestNew_defaults(t *testing.T) {
	t.Parallel()
	c := New(Options{}, nil, nil)
	if c.keychain != authn.DefaultKeychain {
		t.Errorf("keychain = %v, want authn.DefaultKeychain", c.keychain)
	}
	requirePrivateTransport(t, c.transport)
	if other := New(Options{}, nil, nil); other.transport == c.transport {
		t.Error("two clients share one transport")
	}
	if c.userAgent != defaultUserAgent {
		t.Errorf("userAgent = %q, want %q", c.userAgent, defaultUserAgent)
	}
	if c.timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", c.timeout, DefaultTimeout)
	}
}

// TestNew_explicitValues asserts every explicit Options/keychain/transport
// value is used as-is (the boundary opposite of TestNew_defaults): even an
// explicit http.DefaultTransport stays the caller's choice.
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

func TestNew_typedNilDependenciesUseDefaults(t *testing.T) {
	t.Parallel()
	var keychain *typedNilKeychain
	var transport *typedNilTransport
	c := New(Options{}, keychain, transport)
	if c.keychain != authn.DefaultKeychain {
		t.Errorf("keychain = %T, want authn.DefaultKeychain", c.keychain)
	}
	requirePrivateTransport(t, c.transport)
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

// TestNewLimits covers zero and negative (defaults) and explicit values.
func TestNewLimits(t *testing.T) {
	t.Parallel()
	want := limits{
		transfer: DefaultTransferTimeout, manifestBytes: DefaultMaxManifestBytes, blobBytes: DefaultMaxBlobBytes,
		totalBytes: DefaultMaxTotalBytes, blobs: DefaultMaxBlobs, referrers: DefaultMaxReferrers,
	}
	if got := newLimits(Options{}); got != want {
		t.Errorf("zero options = %+v, want %+v", got, want)
	}
	if got := newLimits(Options{MaxBlobBytes: -1, MaxBlobs: -5, TransferTimeout: -time.Second}); got != want {
		t.Errorf("negative options = %+v, want defaults", got)
	}
	explicit := Options{TransferTimeout: time.Minute, MaxManifestBytes: 1, MaxBlobBytes: 2, MaxTotalBytes: 3, MaxBlobs: 4, MaxReferrers: 5}
	if got := newLimits(explicit); got != (limits{time.Minute, 1, 2, 3, 4, 5}) {
		t.Errorf("explicit options = %+v", got)
	}
}

func TestValidateName(t *testing.T) {
	t.Parallel()
	for n, ok := range map[string]bool{
		"model.onnx": true, "a": true, "": false, ".": false, "..": false,
		"../x": false, "a/b": false, `a\b`: false, "/abs": false,
	} {
		if err := validateName(n); (err == nil) != ok {
			t.Errorf("validateName(%q) = %v, want ok=%v", n, err, ok)
		}
	}
}
