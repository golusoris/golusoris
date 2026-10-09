// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	regsrv "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"
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

// TestVerifier covers the stream check every blob passes: exact content,
// short, long and corrupt streams, and cancellation between reads.
func TestVerifier(t *testing.T) {
	t.Parallel()
	body := []byte(strings.Repeat("layer", 1000))
	h, size, err := v1.SHA256(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	d := v1.Descriptor{Digest: h, Size: size}
	read := func(ctx context.Context, b []byte) error {
		_, cerr := io.Copy(io.Discard, newVerifier(ctx, io.NopCloser(bytes.NewReader(b)), d))
		return cerr
	}
	if err = read(t.Context(), body); err != nil {
		t.Fatalf("intact stream: %v", err)
	}
	corrupt := bytes.Clone(body)
	corrupt[7] ^= 1
	for name, b := range map[string][]byte{"short": body[:len(body)-1], "long": append(bytes.Clone(body), 'x'), "corrupt": corrupt} {
		if err = read(t.Context(), b); !errors.Is(err, ErrDigestMismatch) {
			t.Errorf("%s stream err = %v", name, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	v := newVerifier(ctx, io.NopCloser(bytes.NewReader(body)), d)
	if _, err = v.Read(make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err = io.Copy(io.Discard, v); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled mid-stream err = %v", err)
	}
}

// TestLayoutWalkBounds covers the manifest budget of a reachability walk and
// a sweep canceled before it removes anything.
func TestLayoutWalkBounds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	l, err := CreateLayout(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	img, err := l.PushArtifact(t.Context(), Artifact{ArtifactType: "application/vnd.example.a"})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"` + img.Digest.String() + `","size":` + strconv.FormatInt(img.Size, 10) + `}]}`)
	h, size, err := v1.SHA256(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	idx := v1.Descriptor{MediaType: types.OCIImageIndex, Digest: h, Size: size}
	if err = l.writeBlob(idx, bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = l.reachable(t.Context(), []v1.Descriptor{idx}, 2); err != nil {
		t.Fatalf("two manifests at budget 2: %v", err)
	}
	if _, err = l.reachable(t.Context(), []v1.Descriptor{idx}, 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("two manifests at budget 1 err = %v, want ErrTooLarge", err)
	}
	blobs, ok, err := l.openBlobDir()
	if err != nil || !ok {
		t.Fatalf("openBlobDir = %v, %v", ok, err)
	}
	defer func() { _ = blobs.Close() }()
	files, err := blobFiles(blobs)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	rep, err := sweep(canceled, blobs, files, nil, false)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrPartialDelete) || len(rep.Removed) != 0 {
		t.Fatalf("canceled sweep = %+v, %v", rep, err)
	}
	if after, _ := blobFiles(blobs); len(after) != len(files) {
		t.Fatalf("canceled sweep removed %d files", len(files)-len(after))
	}
}

// probeReader and probeTransport call probe on every read or request.
type probeReader struct {
	probe func()
	r     io.Reader
}

func (p probeReader) Read(b []byte) (int, error) {
	p.probe()
	return p.r.Read(b)
}

type probeTransport struct {
	probe func()
	base  http.RoundTripper
}

func (p probeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	p.probe()
	return p.base.RoundTrip(r)
}

// TestLayoutWritersHoldGCLock proves pushes and copies into a layout hold
// the lock GC takes, so GC never sees their blobs before index.json does.
func TestLayoutWritersHoldGCLock(t *testing.T) {
	t.Parallel()
	l, err := CreateLayout(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	var held, free atomic.Int32
	probe := func() {
		if l.gc.TryLock() {
			l.gc.Unlock()
			free.Add(1)
			return
		}
		held.Add(1)
	}
	a := Artifact{ArtifactType: "application/vnd.example.a", Blobs: []Blob{{Reader: probeReader{probe: probe, r: strings.NewReader("x")}}}}
	if _, err = l.PushArtifact(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	if held.Load() == 0 || free.Load() != 0 {
		t.Fatalf("PushArtifact: %d reads under the GC lock, %d without", held.Load(), free.Load())
	}
	srv := httptest.NewServer(regsrv.New())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")
	base := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(base.CloseIdleConnections)
	if _, err = New(Options{}, authn.NewMultiKeychain(), base).PushArtifact(t.Context(), host+"/a:v1", Artifact{ArtifactType: "application/vnd.example.b"}); err != nil {
		t.Fatal(err)
	}
	held.Store(0)
	c := New(Options{}, authn.NewMultiKeychain(), probeTransport{probe: probe, base: base})
	if _, err = c.CopyToLayout(t.Context(), host+"/a:v1", l); err != nil {
		t.Fatal(err)
	}
	if held.Load() == 0 || free.Load() != 0 {
		t.Fatalf("CopyToLayout: %d requests under the GC lock, %d without", held.Load(), free.Load())
	}
}
