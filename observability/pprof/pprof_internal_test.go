// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pprof

import (
	"strings"
	"testing"
)

func TestCredentialDigestHasConstantSize(t *testing.T) {
	t.Parallel()
	short := credentialDigest("x")
	long := credentialDigest(strings.Repeat("x", 4096))
	if len(short) != len(long) {
		t.Fatalf("digest lengths differ: %d != %d", len(short), len(long))
	}
	if short == long {
		t.Fatal("different credentials produced the same digest")
	}
}
