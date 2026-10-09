// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcp_test

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
	other := genKey(t, "EC_SIGN_P256_SHA256").Public()
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
	for _, alg := range []string{"EC_SIGN_P256_SHA256", "EC_SIGN_P384_SHA384", "RSA_SIGN_PKCS1_2048_SHA256", "EC_SIGN_ED25519"} {
		t.Run(alg, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKey("k", alg)
			imageRoundTrip(t, newSigner(t, fakeConfig(addr, "k")))
		})
	}
}

// TestImage_Fails covers a key version sign.Image cannot use (PSS, where
// cosign signs RSA keys with PKCS #1 v1.5) and a denied signature.
func TestImage_Fails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		alg  string
		deny bool
	}{{"RSA_SIGN_PSS_2048_SHA256", false}, {"EC_SIGN_P256_SHA256", true}} {
		t.Run(tc.alg, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			v := f.addKey("k", tc.alg)
			s := newSigner(t, fakeConfig(addr, "k"))
			if pub, ok := v.priv.Public().(interface{ Equal(x crypto.PublicKey) bool }); !ok || !pub.Equal(s.Public()) {
				t.Fatal("Public is not the KMS key")
			}
			if tc.deny {
				f.set(func(f *fakeKMS) {
					f.hook = func(w http.ResponseWriter, _ *http.Request) bool {
						f.fail(w, http.StatusForbidden, "PERMISSION_DENIED", "denied")
						return true
					}
				})
			}
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
				t.Fatal("sign.Image succeeded")
			}
		})
	}
}
