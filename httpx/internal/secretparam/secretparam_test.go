// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secretparam_test

import (
	"testing"

	"github.com/golusoris/golusoris/httpx/internal/secretparam"
)

func TestSharedSet(t *testing.T) {
	t.Parallel()
	set := secretparam.Set(secretparam.Defaults(), []string{"Extra-Secret"})
	for _, name := range []string{"API_KEY", "access-token", "To_ken", "PIN", "password", "client_secret", "X-Plex-Token", "refresh_token", "id_token", "extra_secret"} {
		if _, ok := set[secretparam.CanonicalName(name)]; !ok {
			t.Errorf("secret %q absent", name)
		}
	}
	for _, name := range []string{"", "q", "tokenizer", "extra.secret"} {
		if _, ok := set[secretparam.CanonicalName(name)]; ok {
			t.Errorf("nonsecret %q present", name)
		}
	}
}

func TestDefaultsCallerOwned(t *testing.T) {
	t.Parallel()
	first := secretparam.Defaults()
	want := first[0]
	first[0] = "changed"
	if got := secretparam.Defaults()[0]; got != want {
		t.Fatalf("defaults = %q, want %q", got, want)
	}
}
