// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ui

import "testing"

func TestCredentialEqual(t *testing.T) {
	t.Parallel()

	if !credentialEqual("s3cret", "s3cret") {
		t.Fatal("equal credentials did not match")
	}
	for _, candidate := range []string{"", "s3cre", "s3cret-longer", "different"} {
		if credentialEqual(candidate, "s3cret") {
			t.Fatalf("credential %q unexpectedly matched", candidate)
		}
	}
}
