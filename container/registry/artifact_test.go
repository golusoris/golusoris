// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	regsrv "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/types"

	"github.com/golusoris/golusoris/container/registry"
)

const (
	scoreType = "application/vnd.vmafx.score.v1+json"
	modelType = "application/vnd.vmafx.model.v1"
)

// newArtifactRegistry starts an in-process registry with the given server
// options wrapped by mw (nil for none) and returns its host:port.
func newArtifactRegistry(t *testing.T, mw func(http.Handler) http.Handler, opts ...regsrv.Option) string {
	t.Helper()
	h := regsrv.New(opts...)
	if mw != nil {
		h = mw(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse registry URL: %v", err)
	}
	return u.Host
}

func artifactClient(t *testing.T, opts registry.Options, rt http.RoundTripper) *registry.Client {
	t.Helper()
	if rt == nil {
		rt = newTestTransport(t)
	}
	return registry.New(opts, authn.NewMultiKeychain(), rt)
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func tempFile(t *testing.T, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}

func TestArtifact_RoundTrip(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, nil)
	c := artifactClient(t, registry.Options{}, nil)
	ctx := testCtx(t)
	scores, report := []byte(`{"vmaf":97.1}`), []byte("report body")
	desc, err := c.PushArtifact(ctx, host+"/vmafx/scores:run-1", registry.Artifact{
		ArtifactType: scoreType,
		Blobs: []registry.Blob{
			{MediaType: "application/vnd.vmafx.scores.v1+json", Path: tempFile(t, "scores.json", scores)},
			{Name: "report.txt", Reader: bytes.NewReader(report), Annotations: map[string]string{"k": "v"}},
		},
		Annotations: map[string]string{"dev.vmafx.run": "1"},
	})
	if err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}
	if desc.ArtifactType != scoreType || desc.MediaType != types.OCIManifestSchema1 {
		t.Fatalf("descriptor = %+v", desc)
	}
	resolved, err := c.Resolve(ctx, host+"/vmafx/scores:run-1")
	if err != nil || resolved.DigestStr() != desc.Digest.String() {
		t.Fatalf("Resolve = %v, %v; want %s", resolved, err, desc.Digest)
	}
	dir := filepath.Join(t.TempDir(), "out")
	got, err := c.PullArtifact(ctx, host+"/vmafx/scores@"+desc.Digest.String(), dir, registry.PullOptions{ArtifactType: scoreType})
	if err != nil || got.Digest != desc.Digest {
		t.Fatalf("PullArtifact = %v, %v", got.Digest, err)
	}
	if b := mustRead(t, filepath.Join(dir, "scores.json")); !bytes.Equal(b, scores) {
		t.Fatalf("scores.json = %q", b)
	}
	if b := mustRead(t, filepath.Join(dir, "report.txt")); !bytes.Equal(b, report) {
		t.Fatalf("report.txt = %q", b)
	}
	_, man, err := c.ArtifactManifest(ctx, host+"/vmafx/scores:run-1")
	if err != nil {
		t.Fatalf("ArtifactManifest: %v", err)
	}
	if man.Annotations["dev.vmafx.run"] != "1" || man.Layers[0].MediaType != "application/vnd.vmafx.scores.v1+json" ||
		man.Layers[1].Annotations["k"] != "v" || man.Config.MediaType != registry.EmptyJSONMediaType {
		t.Fatalf("manifest = %+v", man)
	}
}

func TestPushArtifact_StableDigest(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, nil)
	c := artifactClient(t, registry.Options{}, nil)
	ctx := testCtx(t)
	model := tempFile(t, "model.onnx", []byte("weights"))
	push := func(ref string, ann map[string]string) v1.Descriptor {
		t.Helper()
		d, err := c.PushArtifact(ctx, ref, registry.Artifact{
			ArtifactType: modelType, Blobs: []registry.Blob{{MediaType: "application/vnd.vmafx.model.onnx", Path: model}}, Annotations: ann,
		})
		if err != nil {
			t.Fatalf("PushArtifact %s: %v", ref, err)
		}
		return d
	}
	a := push(host+"/vmafx/models", map[string]string{"v": "1"})
	b := push(host+"/vmafx/models:v1", map[string]string{"v": "1"})
	if a.Digest != b.Digest {
		t.Fatalf("identical artifacts got digests %s and %s", a.Digest, b.Digest)
	}
	if d := push(host+"/vmafx/models", map[string]string{"v": "2"}); d.Digest == a.Digest {
		t.Fatal("different annotations produced the same digest")
	}
	_, man, err := c.ArtifactManifest(ctx, host+"/vmafx/models@"+a.Digest.String())
	if err != nil {
		t.Fatalf("untagged push not addressable by digest: %v", err)
	}
	if _, ok := man.Annotations["org.opencontainers.image.created"]; ok {
		t.Fatal("time-dependent annotation breaks digest stability")
	}
}

func TestPushArtifact_EmptyLayer(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, nil)
	c := artifactClient(t, registry.Options{}, nil)
	ctx := testCtx(t)
	desc, err := c.PushArtifact(ctx, host+"/r:empty", registry.Artifact{ArtifactType: scoreType})
	if err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}
	_, man, err := c.ArtifactManifest(ctx, host+"/r@"+desc.Digest.String())
	if err != nil || len(man.Layers) != 1 || man.Layers[0].MediaType != registry.EmptyJSONMediaType {
		t.Fatalf("manifest = %+v, %v", man, err)
	}
}

func TestPullArtifact_DigestMismatchRejected(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, nil)
	ctx := testCtx(t)
	desc, err := artifactClient(t, registry.Options{}, nil).PushArtifact(ctx, host+"/r:t", registry.Artifact{
		ArtifactType: scoreType,
		Blobs:        []registry.Blob{{Name: "a.bin", Reader: strings.NewReader("payload")}},
	})
	if err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}
	_, man, err := artifactClient(t, registry.Options{}, nil).ArtifactManifest(ctx, host+"/r:t")
	if err != nil {
		t.Fatalf("ArtifactManifest: %v", err)
	}
	bad := artifactClient(t, registry.Options{}, &corruptingTransport{base: newTestTransport(t), pathSuffix: "/blobs/" + man.Layers[0].Digest.String()})
	dir := t.TempDir()
	if _, err = bad.PullArtifact(ctx, host+"/r@"+desc.Digest.String(), dir, registry.PullOptions{}); !errors.Is(err, registry.ErrDigestMismatch) {
		t.Fatalf("PullArtifact err = %v, want ErrDigestMismatch", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("unverified content left behind: %v", entries)
	}
	if _, err = bad.FetchBlob(ctx, host+"/r", man.Layers[0], 1<<20); !errors.Is(err, registry.ErrDigestMismatch) {
		t.Fatalf("FetchBlob err = %v, want ErrDigestMismatch", err)
	}
}

func TestArtifact_SizeCaps(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, nil)
	ctx := testCtx(t)
	small := artifactClient(t, registry.Options{MaxBlobBytes: 8}, nil)
	blob := func(body string) registry.Artifact {
		return registry.Artifact{ArtifactType: scoreType, Blobs: []registry.Blob{{Name: "b", Reader: strings.NewReader(body)}}}
	}
	// Boundary: exactly MaxBlobBytes pushes and pulls.
	ok, err := small.PushArtifact(ctx, host+"/r:eight", blob("12345678"))
	if err != nil {
		t.Fatalf("push at limit: %v", err)
	}
	if _, err = small.PullArtifact(ctx, host+"/r@"+ok.Digest.String(), t.TempDir(), registry.PullOptions{}); err != nil {
		t.Fatalf("pull at limit: %v", err)
	}
	if _, err = small.PushArtifact(ctx, host+"/r:nine", blob("123456789")); !errors.Is(err, registry.ErrTooLarge) {
		t.Fatalf("reader over limit err = %v", err)
	}
	pathBlob := registry.Artifact{ArtifactType: scoreType, Blobs: []registry.Blob{{Path: tempFile(t, "n", []byte("123456789"))}}}
	if _, err = small.PushArtifact(ctx, host+"/r:nine", pathBlob); !errors.Is(err, registry.ErrTooLarge) {
		t.Fatalf("path over limit err = %v", err)
	}
	big, err := artifactClient(t, registry.Options{}, nil).PushArtifact(ctx, host+"/r:sixteen", blob("0123456789abcdef"))
	if err != nil {
		t.Fatalf("push big: %v", err)
	}
	cases := map[string]*registry.Client{
		"blob":     small,
		"total":    artifactClient(t, registry.Options{MaxTotalBytes: 15}, nil),
		"manifest": artifactClient(t, registry.Options{MaxManifestBytes: 64}, nil),
		"count":    artifactClient(t, registry.Options{MaxBlobs: 1}, nil),
	}
	two, err := artifactClient(t, registry.Options{}, nil).PushArtifact(ctx, host+"/r:two", registry.Artifact{
		ArtifactType: scoreType,
		Blobs:        []registry.Blob{{Reader: strings.NewReader("a")}, {Reader: strings.NewReader("b")}},
	})
	if err != nil {
		t.Fatalf("push two: %v", err)
	}
	for name, c := range cases {
		target := big
		if name == "count" {
			target = two
		}
		if _, err = c.PullArtifact(ctx, host+"/r@"+target.Digest.String(), t.TempDir(), registry.PullOptions{}); !errors.Is(err, registry.ErrTooLarge) {
			t.Errorf("%s cap: pull err = %v, want ErrTooLarge", name, err)
		}
	}
	if _, err = cases["count"].PushArtifact(ctx, host+"/r:x", registry.Artifact{
		ArtifactType: scoreType,
		Blobs:        []registry.Blob{{Reader: strings.NewReader("a")}, {Reader: strings.NewReader("b")}},
	}); !errors.Is(err, registry.ErrTooLarge) {
		t.Fatalf("push count err = %v", err)
	}
	_, man, err := small.ArtifactManifest(ctx, host+"/r:sixteen")
	if err != nil {
		t.Fatalf("ArtifactManifest: %v", err)
	}
	if _, err = artifactClient(t, registry.Options{}, nil).FetchBlob(ctx, host+"/r", man.Layers[0], 15); !errors.Is(err, registry.ErrTooLarge) {
		t.Fatalf("FetchBlob over maxBytes err = %v", err)
	}
}

func TestPullArtifact_ArtifactTypeMismatch(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, nil)
	c := artifactClient(t, registry.Options{}, nil)
	ctx := testCtx(t)
	if _, err := c.PushArtifact(ctx, host+"/r:t", registry.Artifact{ArtifactType: scoreType}); err != nil {
		t.Fatalf("PushArtifact: %v", err)
	}
	_, err := c.PullArtifact(ctx, host+"/r:t", t.TempDir(), registry.PullOptions{ArtifactType: modelType})
	if !errors.Is(err, registry.ErrArtifactType) {
		t.Fatalf("err = %v, want ErrArtifactType", err)
	}
}

func TestArtifact_InvalidInputs(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, nil)
	c := artifactClient(t, registry.Options{}, nil)
	ctx := testCtx(t)
	push := func(ref string, a registry.Artifact) func() error {
		return func() error { _, err := c.PushArtifact(ctx, ref, a); return err }
	}
	cases := map[string]func() error{
		"digest target":    push(host+"/r@sha256:"+strings.Repeat("a", 64), registry.Artifact{ArtifactType: scoreType}),
		"no artifact type": push(host+"/r", registry.Artifact{}),
		"path and reader":  push(host+"/r", registry.Artifact{ArtifactType: scoreType, Blobs: []registry.Blob{{Path: "x", Reader: strings.NewReader("y")}}}),
		"no blob source":   push(host+"/r", registry.Artifact{ArtifactType: scoreType, Blobs: []registry.Blob{{}}}),
		"traversal name":   push(host+"/r", registry.Artifact{ArtifactType: scoreType, Blobs: []registry.Blob{{Name: "../evil", Reader: strings.NewReader("y")}}}),
		"bad repository":   push("UPPER/Case::", registry.Artifact{ArtifactType: scoreType}),
		"fetch bad repo": func() error {
			_, err := c.FetchBlob(ctx, "::bad", v1.Descriptor{}, 1)
			return err
		},
	}
	for name, fn := range cases {
		if err := fn(); !errors.Is(err, registry.ErrInvalidArtifact) {
			t.Errorf("%s: err = %v, want ErrInvalidArtifact", name, err)
		}
	}
}

// basicAuth demands robot:s3cret on every request.
func basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "robot" || p != "s3cret" {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TestPushArtifact_Keychain(t *testing.T) {
	t.Parallel()
	host := newArtifactRegistry(t, basicAuth)
	ctx := testCtx(t)
	kc := staticKeychain{auth: authn.FromConfig(authn.AuthConfig{Username: "robot", Password: "s3cret"})}
	c := registry.New(registry.Options{}, kc, newTestTransport(t))
	if _, err := c.PushArtifact(ctx, host+"/r:t", registry.Artifact{ArtifactType: scoreType}); err != nil {
		t.Fatalf("PushArtifact with keychain: %v", err)
	}
	if _, err := artifactClient(t, registry.Options{}, nil).PushArtifact(ctx, host+"/r:t", registry.Artifact{ArtifactType: scoreType}); err == nil {
		t.Fatal("anonymous push succeeded against an authenticated registry")
	}
}
