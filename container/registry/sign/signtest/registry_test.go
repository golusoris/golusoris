// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package signtest_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/container/registry/sign/signtest"
)

func TestNewRegistryImage(t *testing.T) {
	t.Parallel()
	ctx, client, ref := signtest.NewRegistryImage(t)
	if ctx.Err() != nil || client == nil {
		t.Fatalf("NewRegistryImage = ctx err %v, client %v", ctx.Err(), client)
	}
	if !strings.Contains(ref, "/app@sha256:") {
		t.Fatalf("ref = %q, want a digest reference", ref)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	signer := mustKey(t)
	signtest.RoundTrip(t, signer, mustKey(t).Public())
}

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
