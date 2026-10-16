// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package aws_test

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
	signtest.RoundTrip(t, signer, genKey(t, "ECC_NIST_P256").Public())
}

func TestImage_RoundTripFake(t *testing.T) {
	t.Parallel()
	for _, spec := range []string{"ECC_NIST_P256", "ECC_NIST_P384", "ECC_NIST_P521", "RSA_2048", "ECC_NIST_EDWARDS25519"} {
		t.Run(spec, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKey("k", spec)
			imageRoundTrip(t, newSigner(t, fakeConfig(addr, "k")))
		})
	}
}

func TestImage_FailsWithoutKMS(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	k := f.addKey("k", "ECC_NIST_P256")
	s := newSigner(t, fakeConfig(addr, "k"))
	if !k.priv.Public().(*ecdsa.PublicKey).Equal(s.Public()) {
		t.Fatal("Public is not the KMS key")
	}
	f.set(func(f *fakeKMS) {
		f.hook = func(w http.ResponseWriter, _ *http.Request, _ string) bool {
			f.fail(w, "AccessDeniedException", "not authorized")
			return true
		}
	})
	ctx, c, ref := signtest.NewRegistryImage(t)
	if _, err := sign.Image(ctx, c, ref, sign.Signer{Key: s}, sign.Options{}); err == nil {
		t.Fatal("sign.Image succeeded although KMS denied the signature")
	}
}
