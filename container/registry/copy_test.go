// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package registry_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/golusoris/golusoris/container/registry"
)

// TestClient_Copy_crossRegistry pushes an image into one in-process registry
// and copies it to a second, independent one, then verifies the destination
// carries the identical manifest.
func TestClient_Copy_crossRegistry(t *testing.T) {
	t.Parallel()
	srcHost := newTestRegistry(t)
	dstHost := newTestRegistry(t)
	img := mustRandomImage(t)
	wantDigest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}
	src := srcHost + "/test/copy:v1"
	dst := dstHost + "/test/copy:v1"
	pushImage(t, src, img)

	c := newAnonClient()
	if err = c.Copy(t.Context(), src, dst); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	dstRef, err := name.ParseReference(dst)
	if err != nil {
		t.Fatalf("parse dst reference: %v", err)
	}
	desc, err := remote.Get(dstRef, remote.WithAuth(authn.Anonymous))
	if err != nil {
		t.Fatalf("verify copied image: %v", err)
	}
	if desc.Digest.String() != wantDigest.String() {
		t.Fatalf("copied digest = %s, want %s", desc.Digest, wantDigest)
	}
}

// TestClient_Copy_missingSource covers the negative path: copying a
// reference that doesn't exist on the source registry fails without ever
// reaching the destination.
func TestClient_Copy_missingSource(t *testing.T) {
	t.Parallel()
	srcHost := newTestRegistry(t)
	dstHost := newTestRegistry(t)
	src := srcHost + "/test/does-not-exist:v1"
	dst := dstHost + "/test/copy:v1"

	c := newAnonClient()
	err := c.Copy(t.Context(), src, dst)
	if err == nil {
		t.Fatal("Copy: expected error for missing source image, got nil")
	}
	var terr *transport.Error
	if !errors.As(err, &terr) {
		t.Fatalf("Copy: expected *transport.Error, got %T: %v", err, err)
	}
	if terr.StatusCode != http.StatusNotFound {
		t.Fatalf("Copy: status = %d, want %d", terr.StatusCode, http.StatusNotFound)
	}
}

// corruptingTransport flips a byte in the response body of exactly one GET
// (identified by its URL path suffix), simulating a registry — or a
// man-in-the-middle — that serves content not matching the digest the
// client asked for. It exists to exercise [registry.Client]'s injectable
// transport: production code never does this, but a caller who needs to
// harden against it (or, as here, test the failure path) can.
type corruptingTransport struct {
	pathSuffix string
}

func (c *corruptingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil || req.Method != http.MethodGet || !strings.HasSuffix(req.URL.Path, c.pathSuffix) {
		return resp, err
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if len(body) > 0 {
		body[0] ^= 0xFF
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp, nil
}

// TestClient_Manifest_digestMismatch is the boundary test: a manifest fetched
// by digest whose response body no longer hashes to that digest (here,
// forced via the injected transport) must be rejected, not silently
// accepted.
func TestClient_Manifest_digestMismatch(t *testing.T) {
	t.Parallel()
	host := newTestRegistry(t)
	img := mustRandomImage(t)
	digest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}
	repo := host + "/test/mismatch"
	pushImage(t, repo+":v1", img)

	rt := &corruptingTransport{pathSuffix: "/manifests/" + digest.String()}
	c := registry.New(registry.Options{}, authn.NewMultiKeychain(), rt)

	ref := fmt.Sprintf("%s@%s", repo, digest)
	_, err = c.Manifest(t.Context(), ref)
	if err == nil {
		t.Fatal("Manifest: expected digest-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "does not match requested digest") {
		t.Fatalf("Manifest: expected digest-mismatch error, got: %v", err)
	}
}
