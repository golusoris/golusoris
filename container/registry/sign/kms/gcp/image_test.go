// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcp_test

import (
	"crypto"
	"net/http"
	"testing"

	"github.com/golusoris/golusoris/container/registry/sign"
	"github.com/golusoris/golusoris/container/registry/sign/signtest"
)

func imageRoundTrip(t *testing.T, signer crypto.Signer) {
	t.Helper()
	signtest.RoundTrip(t, signer, genKey(t, "EC_SIGN_P256_SHA256").Public())
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
			ctx, c, ref := signtest.NewRegistryImage(t)
			if _, err := sign.Image(ctx, c, ref, sign.Signer{Key: s}, sign.Options{}); err == nil {
				t.Fatal("sign.Image succeeded")
			}
		})
	}
}
