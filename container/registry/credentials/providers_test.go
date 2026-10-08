// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package credentials_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/container/registry/credentials"
	"github.com/golusoris/golusoris/core/clock"
)

func secretFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestStaticFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	user := secretFile(t, dir, "user", "robot$ci\n")
	pass := secretFile(t, dir, "pass", "s3cret\n")
	tok := secretFile(t, dir, "tok", "identity-token")
	s, err := credentials.NewStaticFiles([]credentials.StaticOptions{
		{Host: "harbor.example", UsernameFile: user, PasswordFile: pass},
		{Host: "ghcr.io", Username: "bot", PasswordFile: pass},
		{Host: "reg.example:5000", IdentityTokenFile: tok},
	})
	if err != nil {
		t.Fatalf("NewStaticFiles: %v", err)
	}
	ctx := t.Context()
	want := map[string]credentials.Credential{
		"harbor.example":   {Username: "robot$ci", Password: "s3cret"},
		"ghcr.io":          {Username: "bot", Password: "s3cret"},
		"reg.example:5000": {RefreshToken: "identity-token"},
	}
	for host, w := range want {
		got, cerr := s.Credential(ctx, host)
		if cerr != nil || got != w {
			t.Errorf("%s = %+v, %v; want %+v", host, got, cerr, w)
		}
	}
	if _, err = s.Credential(ctx, "other.example"); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("unknown host err = %v", err)
	}
	// Rotation: the file is re-read on every lookup.
	secretFile(t, dir, "pass", "rotated")
	if got, _ := s.Credential(ctx, "ghcr.io"); got.Password != "rotated" {
		t.Fatalf("rotated password = %q", got.Password)
	}
}

func TestStaticFiles_Invalid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pass := secretFile(t, dir, "pass", "x")
	cases := map[string][]credentials.StaticOptions{
		"empty host":         {{PasswordFile: pass, Username: "u"}},
		"no secret":          {{Host: "h"}},
		"both secrets":       {{Host: "h", Username: "u", PasswordFile: pass, IdentityTokenFile: pass}},
		"password no user":   {{Host: "h", PasswordFile: pass}},
		"user and user file": {{Host: "h", Username: "u", UsernameFile: pass, PasswordFile: pass}},
		"duplicate host":     {{Host: "h", Username: "u", PasswordFile: pass}, {Host: "h", IdentityTokenFile: pass}},
	}
	for name, entries := range cases {
		if _, err := credentials.NewStaticFiles(entries); !errors.Is(err, credentials.ErrInvalidStatic) {
			t.Errorf("%s: err = %v, want ErrInvalidStatic", name, err)
		}
	}
}

func TestStaticFiles_SecretBounds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	limit := 64 << 10
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"at limit", strings.Repeat("a", limit), false},
		{"over limit", strings.Repeat("a", limit+1), true},
		{"blank", " \n", true},
	}
	for _, tc := range cases {
		p := secretFile(t, dir, strings.ReplaceAll(tc.name, " ", "-"), tc.body)
		s, err := credentials.NewStaticFiles([]credentials.StaticOptions{{Host: "h", IdentityTokenFile: p}})
		if err != nil {
			t.Fatalf("%s: NewStaticFiles: %v", tc.name, err)
		}
		_, err = s.Credential(t.Context(), "h")
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
	s, err := credentials.NewStaticFiles([]credentials.StaticOptions{{Host: "h", IdentityTokenFile: filepath.Join(dir, "missing")}})
	if err != nil {
		t.Fatalf("NewStaticFiles: %v", err)
	}
	if _, err = s.Credential(t.Context(), "h"); err == nil || errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("missing file err = %v, want hard error", err)
	}
}

func TestCache(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	var calls atomic.Int32
	expiring := credentials.ProviderFunc(func(_ context.Context, host string) (credentials.Credential, error) {
		calls.Add(1)
		switch host {
		case "exp.example":
			return credentials.Credential{Password: "p", ExpiresAt: clk.Now().Add(time.Hour)}, nil
		case "static.example":
			return credentials.Credential{Password: "p"}, nil
		default:
			return credentials.Credential{}, credentials.ErrNoCredential
		}
	})
	c := credentials.NewCache(expiring, clk, 10*time.Minute)
	ctx := t.Context()
	for range 3 {
		if _, err := c.Credential(ctx, "exp.example"); err != nil {
			t.Fatalf("Credential: %v", err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", n)
	}
	// Boundary: 50m in, 10m skew reaches expiry, so the entry is stale.
	clk.Advance(50 * time.Minute)
	if _, err := c.Credential(ctx, "exp.example"); err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("calls after skew = %d, want 2 (refetched)", n)
	}
	for range 2 {
		_, _ = c.Credential(ctx, "static.example")
	}
	if n := calls.Load(); n != 4 {
		t.Fatalf("calls for unexpiring cred = %d, want 4 (never cached)", n)
	}
	if _, err := c.Credential(ctx, "none.example"); !errors.Is(err, credentials.ErrNoCredential) {
		t.Fatalf("err = %v, want ErrNoCredential passthrough", err)
	}
}
