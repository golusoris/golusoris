// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package vault_test

import (
	"crypto"
	"crypto/ecdsa"
	"testing"

	"github.com/golusoris/golusoris/container/registry/sign"
	"github.com/golusoris/golusoris/container/registry/sign/signtest"
)

func imageRoundTrip(t *testing.T, signer crypto.Signer) {
	t.Helper()
	signtest.RoundTrip(t, signer, genKey(t, "ecdsa-p256").Public())
}

func TestImage_RoundTripFake(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"ecdsa-p256", "ecdsa-p384", "rsa-2048", "ed25519"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			f, addr := newFake(t)
			f.addKey("k", typ, 1)
			imageRoundTrip(t, newSigner(t, tokenConfig(addr)))
		})
	}
}

func TestImage_FailsWithoutServer(t *testing.T) {
	t.Parallel()
	f, addr := newFake(t)
	k := f.addKey("k", "ecdsa-p256", 1)
	s := newSigner(t, tokenConfig(addr))
	if !k.versions[1].Public().(*ecdsa.PublicKey).Equal(s.Public()) {
		t.Fatal("Public is not the transit key")
	}
	f.set(func(f *fakeTransit) { f.deny = 1 })
	ctx, c, ref := signtest.NewRegistryImage(t)
	if _, err := sign.Image(ctx, c, ref, sign.Signer{Key: s}, sign.Options{}); err == nil {
		t.Fatal("sign.Image succeeded although transit denied the signature")
	}
}
