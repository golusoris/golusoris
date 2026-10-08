// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package credentials_test

import (
	"context"
	"errors"
	"testing"

	"github.com/golusoris/golusoris/container/registry/credentials"
)

func fixed(host string, cred credentials.Credential) credentials.Provider {
	return credentials.ProviderFunc(func(_ context.Context, h string) (credentials.Credential, error) {
		if h != host {
			return credentials.Credential{}, credentials.ErrNoCredential
		}
		return cred, nil
	})
}

func TestChain(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	failing := credentials.ProviderFunc(func(context.Context, string) (credentials.Credential, error) {
		return credentials.Credential{}, boom
	})
	chain := credentials.Chain{
		nil,
		fixed("a.example", credentials.Credential{Username: "first"}),
		fixed("a.example", credentials.Credential{Username: "second"}),
		fixed("b.example", credentials.Credential{RefreshToken: "tok"}),
		failing,
	}
	ctx := t.Context()

	got, err := chain.Credential(ctx, "a.example")
	if err != nil || got.Username != "first" {
		t.Fatalf("a.example = %+v, %v; want first match", got, err)
	}
	got, err = chain.Credential(ctx, "b.example")
	if err != nil || got.RefreshToken != "tok" {
		t.Fatalf("b.example = %+v, %v", got, err)
	}
	if _, err = chain.Credential(ctx, "c.example"); !errors.Is(err, boom) {
		t.Fatalf("c.example err = %v, want provider error to stop the chain", err)
	}
	if _, err = (credentials.Chain{}).Credential(ctx, "a.example"); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("empty chain err = %v, want ErrNoCredential", err)
	}
}

func TestCredentialIsZero(t *testing.T) {
	t.Parallel()
	if !(credentials.Credential{}).IsZero() {
		t.Fatal("zero credential not reported zero")
	}
	for _, c := range []credentials.Credential{{Username: "u"}, {Password: "p"}, {RefreshToken: "r"}, {AccessToken: "a"}} {
		if c.IsZero() {
			t.Fatalf("%+v reported zero", c)
		}
	}
}
