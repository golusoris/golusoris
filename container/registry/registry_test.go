// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	regsrv "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/golusoris/golusoris/container/registry"
)

// newTestRegistry starts an in-process OCI/Docker registry
// ([regsrv.New]) and returns its "host:port" for building test references.
// The server is closed automatically when the test ends.
func newTestRegistry(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(regsrv.New())
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test registry URL: %v", err)
	}
	return u.Host
}

// mustRandomImage builds a small deterministic-shape (but random-content)
// test image.
func mustRandomImage(t *testing.T) v1.Image {
	t.Helper()
	img, err := random.Image(256, 2)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	return img
}

// pushImage writes img to ref directly via [remote.Write] — test fixture
// setup, independent of the [registry.Client] under test.
func pushImage(t *testing.T, ref string, img v1.Image) {
	t.Helper()
	r, err := name.ParseReference(ref)
	if err != nil {
		t.Fatalf("parse reference %q: %v", ref, err)
	}
	if err := remote.Write(r, img); err != nil {
		t.Fatalf("seed push %q: %v", ref, err)
	}
}

// newAnonClient builds a [registry.Client] against an unauthenticated
// registry with a short, test-appropriate timeout.
func newAnonClient() *registry.Client {
	return registry.New(registry.Options{}, authn.NewMultiKeychain(), http.DefaultTransport)
}

func TestClient_Resolve(t *testing.T) {
	t.Parallel()
	host := newTestRegistry(t)
	img := mustRandomImage(t)
	wantDigest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}
	ref := host + "/test/resolve:v1"
	pushImage(t, ref, img)

	c := newAnonClient()
	got, err := c.Resolve(t.Context(), ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.DigestStr() != wantDigest.String() {
		t.Fatalf("Resolve digest = %s, want %s", got.DigestStr(), wantDigest)
	}
}

func TestClient_Resolve_missingImage(t *testing.T) {
	t.Parallel()
	host := newTestRegistry(t)
	ref := host + "/test/does-not-exist:v1"

	c := newAnonClient()
	_, err := c.Resolve(t.Context(), ref)
	if err == nil {
		t.Fatal("Resolve: expected error for missing image, got nil")
	}
	var terr *transport.Error
	if !errors.As(err, &terr) {
		t.Fatalf("Resolve: expected *transport.Error, got %T: %v", err, err)
	}
	if terr.StatusCode != http.StatusNotFound {
		t.Fatalf("Resolve: status = %d, want %d", terr.StatusCode, http.StatusNotFound)
	}
}

func TestClient_Manifest(t *testing.T) {
	t.Parallel()
	host := newTestRegistry(t)
	img := mustRandomImage(t)
	wantRaw, err := img.RawManifest()
	if err != nil {
		t.Fatalf("img.RawManifest: %v", err)
	}
	wantDigest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}
	ref := host + "/test/manifest:v1"
	pushImage(t, ref, img)

	c := newAnonClient()
	man, err := c.Manifest(t.Context(), ref)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if man.Digest.String() != wantDigest.String() {
		t.Fatalf("Manifest digest = %s, want %s", man.Digest, wantDigest)
	}
	if string(man.Raw) != string(wantRaw) {
		t.Fatalf("Manifest raw bytes did not round-trip")
	}
	if man.Size != int64(len(wantRaw)) {
		t.Fatalf("Manifest size = %d, want %d", man.Size, len(wantRaw))
	}

	// Fetching the same manifest again by its now-known digest must succeed
	// and validate cleanly (the non-mismatch counterpart of the boundary test
	// in copy_test.go).
	byDigest := fmt.Sprintf("%s/test/manifest@%s", host, man.Digest)
	if _, err := c.Manifest(t.Context(), byDigest); err != nil {
		t.Fatalf("Manifest by digest: %v", err)
	}
}

func TestClient_Manifest_missingImage(t *testing.T) {
	t.Parallel()
	host := newTestRegistry(t)
	ref := host + "/test/does-not-exist:v1"

	c := newAnonClient()
	_, err := c.Manifest(t.Context(), ref)
	if err == nil {
		t.Fatal("Manifest: expected error for missing image, got nil")
	}
	var terr *transport.Error
	if !errors.As(err, &terr) {
		t.Fatalf("Manifest: expected *transport.Error, got %T: %v", err, err)
	}
	if terr.StatusCode != http.StatusNotFound {
		t.Fatalf("Manifest: status = %d, want %d", terr.StatusCode, http.StatusNotFound)
	}
}

func TestClient_ListTags(t *testing.T) {
	t.Parallel()
	host := newTestRegistry(t)
	repo := host + "/test/tags"
	img := mustRandomImage(t)
	pushImage(t, repo+":v1", img)
	pushImage(t, repo+":v2", img)
	pushImage(t, repo+":latest", img)

	c := newAnonClient()
	tags, err := c.ListTags(t.Context(), repo)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	want := map[string]bool{"v1": true, "v2": true, "latest": true}
	if len(tags) != len(want) {
		t.Fatalf("ListTags = %v, want 3 tags matching %v", tags, want)
	}
	for _, tag := range tags {
		if !want[tag] {
			t.Fatalf("ListTags: unexpected tag %q in %v", tag, tags)
		}
	}
}

func TestClient_ListTags_missingRepo(t *testing.T) {
	t.Parallel()
	host := newTestRegistry(t)
	repo := host + "/test/does-not-exist"

	c := newAnonClient()
	_, err := c.ListTags(t.Context(), repo)
	if err == nil {
		t.Fatal("ListTags: expected error for missing repository, got nil")
	}
	var terr *transport.Error
	if !errors.As(err, &terr) {
		t.Fatalf("ListTags: expected *transport.Error, got %T: %v", err, err)
	}
	if terr.StatusCode != http.StatusNotFound {
		t.Fatalf("ListTags: status = %d, want %d", terr.StatusCode, http.StatusNotFound)
	}
}

func TestParseReference(t *testing.T) {
	t.Parallel()
	if _, err := registry.ParseReference("gcr.io/proj/img:v1"); err != nil {
		t.Fatalf("ParseReference: %v", err)
	}
	if _, err := registry.ParseReference("not a valid ref!!"); err == nil {
		t.Fatal("ParseReference: expected error for malformed reference, got nil")
	}
}
