// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package signtest provides shared test setup for registry signing tests.
package signtest

import (
	"context"
	"crypto"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	regsrv "github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/golusoris/golusoris/container/registry"
	"github.com/golusoris/golusoris/container/registry/sign"
)

// NewRegistryImage creates a bounded in-process registry with a seeded image.
func NewRegistryImage(t *testing.T) (context.Context, *registry.Client, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	srv := httptest.NewServer(regsrv.New())
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Keep registry transport private because httptest.Server.Close resets http.DefaultTransport.
	rt := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(rt.CloseIdleConnections)
	ref := seedImage(t, ctx, u.Host, rt)
	client := registry.New(registry.Options{}, authn.NewMultiKeychain(), rt)
	return ctx, client, ref
}

// RoundTrip signs and verifies an image, including rejection of a foreign key.
func RoundTrip(t *testing.T, signer crypto.Signer, foreignKey crypto.PublicKey) {
	t.Helper()
	ctx, client, ref := NewRegistryImage(t)
	if _, err := sign.Image(ctx, client, ref, sign.Signer{Key: signer}, sign.Options{}); err != nil {
		t.Fatalf("sign.Image: %v", err)
	}
	sigs, err := sign.Verify(ctx, client, ref, sign.Policy{Key: signer.Public(), InsecureIgnoreTlog: true})
	if err != nil || len(sigs) != 1 {
		t.Fatalf("sign.Verify = %d signatures, %v; want 1", len(sigs), err)
	}
	if _, err := sign.Verify(ctx, client, ref, sign.Policy{Key: foreignKey, InsecureIgnoreTlog: true}); !errors.Is(err, sign.ErrNoValidSignature) {
		t.Fatalf("sign.Verify with a foreign key = %v, want ErrNoValidSignature", err)
	}
}

func seedImage(t *testing.T, ctx context.Context, host string, rt http.RoundTripper) string {
	t.Helper()
	tag, err := name.NewTag(host + "/app:v1")
	if err != nil {
		t.Fatal(err)
	}
	img, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}
	err = remote.Write(tag, img, remote.WithTransport(rt), remote.WithContext(ctx))
	if err != nil {
		t.Fatalf("seed image: %v", err)
	}
	h, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return host + "/app@" + h.String()
}
