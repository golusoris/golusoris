// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azure_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
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

// imageRoundTrip pushes a random image to an in-process registry, signs it
// with s through sign.Image and verifies it through sign.Verify with s's
// public key, as `cosign verify --key --insecure-ignore-tlog` would.
func imageRoundTrip(t *testing.T, s crypto.Signer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	srv := httptest.NewServer(regsrv.New())
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Private transport: httptest.Server.Close resets http.DefaultTransport.
	rt := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(rt.CloseIdleConnections)
	ref := seedImage(t, u.Host, rt)
	c := registry.New(registry.Options{}, authn.NewMultiKeychain(), rt)
	if _, serr := sign.Image(ctx, c, ref, sign.Signer{Key: s}, sign.Options{}); serr != nil {
		t.Fatalf("sign.Image: %v", serr)
	}
	sigs, err := sign.Verify(ctx, c, ref, sign.Policy{Key: s.Public(), InsecureIgnoreTlog: true})
	if err != nil || len(sigs) != 1 {
		t.Fatalf("sign.Verify = %d signatures, %v; want 1", len(sigs), err)
	}
	other := genKey(t, "P-256").Public()
	if _, err := sign.Verify(ctx, c, ref, sign.Policy{Key: other, InsecureIgnoreTlog: true}); !errors.Is(err, sign.ErrNoValidSignature) {
		t.Fatalf("sign.Verify with a foreign key = %v, want ErrNoValidSignature", err)
	}
}

func seedImage(t *testing.T, host string, rt http.RoundTripper) string {
	t.Helper()
	tag, err := name.NewTag(host + "/app:v1")
	if err != nil {
		t.Fatal(err)
	}
	img, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}
	if werr := remote.Write(tag, img, remote.WithTransport(rt), remote.WithContext(t.Context())); werr != nil {
		t.Fatalf("seed image: %v", werr)
	}
	h, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return host + "/app@" + h.String()
}

func TestImage_RoundTripFake(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"P-256", "P-384", "P-521", "RSA"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f, rt := newFake(t)
			f.addKey("k", kind, 1)
			imageRoundTrip(t, newSigner(t, fakeConfig(rt, "k")))
		})
	}
}

func TestImage_FailsWithoutVault(t *testing.T) {
	t.Parallel()
	f, vrt := newFake(t)
	v := f.addKey("k", "P-256", 1)[0]
	s := newSigner(t, fakeConfig(vrt, "k"))
	if !v.priv.Public().(*ecdsa.PublicKey).Equal(s.Public()) {
		t.Fatal("Public is not the vault key")
	}
	f.set(func(f *fakeVault) {
		f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
			f.fail(w, http.StatusForbidden, "Forbidden", "denied")
			return true
		}
	})
	srv := httptest.NewServer(regsrv.New())
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	rt := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(rt.CloseIdleConnections)
	ref := seedImage(t, u.Host, rt)
	c := registry.New(registry.Options{}, authn.NewMultiKeychain(), rt)
	if _, err := sign.Image(t.Context(), c, ref, sign.Signer{Key: s}, sign.Options{}); err == nil {
		t.Fatal("sign.Image succeeded although the vault denied the signature")
	}
}
