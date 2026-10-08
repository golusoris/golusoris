// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import "testing"

func TestFormatPostgresDSNEscapesCredentialsAndAddress(t *testing.T) {
	t.Parallel()
	got := formatPostgresDSN("2001:db8::1", 5432, "p@ss/word")
	want := "postgres://appuser:p%40ss%2Fword@[2001:db8::1]:5432/app?sslmode=require"
	if got != want {
		t.Fatalf("formatPostgresDSN() = %q, want %q", got, want)
	}
}
