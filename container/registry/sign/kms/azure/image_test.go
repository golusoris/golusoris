// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package azure_test

import (
	"crypto"
	"crypto/ecdsa"
	"net/http"
	"testing"

	"github.com/golusoris/golusoris/container/registry/sign"
	"github.com/golusoris/golusoris/container/registry/sign/signtest"
)

func imageRoundTrip(t *testing.T, signer crypto.Signer) {
	t.Helper()
	signtest.RoundTrip(t, signer, genKey(t, "P-256").Public())
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
	ctx, c, ref := signtest.NewRegistryImage(t)
	if _, err := sign.Image(ctx, c, ref, sign.Signer{Key: s}, sign.Options{}); err == nil {
		t.Fatal("sign.Image succeeded although the vault denied the signature")
	}
}
