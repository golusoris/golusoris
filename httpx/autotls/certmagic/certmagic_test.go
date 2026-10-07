// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package certmagic_test

import (
	"crypto/tls"
	"slices"
	"strings"
	"testing"
	"time"

	cm "github.com/caddyserver/certmagic"

	"github.com/golusoris/golusoris/httpx/autotls/certmagic"
)

func TestNewRequiresDomains(t *testing.T) {
	t.Parallel()
	_, err := certmagic.New(certmagic.Options{})
	if err == nil || !strings.Contains(err.Error(), "Domains required") {
		t.Fatalf("New: got %v", err)
	}
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts certmagic.Options
		want string
	}{
		{
			name: "negative timeout",
			opts: certmagic.Options{Domains: []string{"example.test"}, Timeout: -time.Second},
			want: "Timeout must not be negative",
		},
		{
			name: "excessive timeout",
			opts: certmagic.Options{Domains: []string{"example.test"}, Timeout: certmagic.MaxStartTimeout + time.Second},
			want: "Timeout exceeds",
		},
		{
			name: "empty domain",
			opts: certmagic.Options{Domains: []string{"example.test", " "}},
			want: "Domains[1]",
		},
		{
			name: "oversized domain",
			opts: certmagic.Options{Domains: []string{strings.Repeat("a", certmagic.MaxDomainBytes+1)}},
			want: "exceeds",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := certmagic.New(test.opts)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New: got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestNewRejectsTypedNilStorage(t *testing.T) {
	t.Parallel()

	var storage *cm.FileStorage
	_, err := certmagic.New(certmagic.Options{
		Domains: []string{"example.test"},
		Storage: storage,
	})
	if err == nil || !strings.Contains(err.Error(), "typed nil") {
		t.Fatalf("New: got %v", err)
	}
}

func TestNewBoundsDomains(t *testing.T) {
	t.Parallel()

	domains := make([]string, certmagic.MaxDomains+1)
	for i := range domains {
		domains[i] = "example.test"
	}
	_, err := certmagic.New(certmagic.Options{Domains: domains})
	if err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("New: got %v", err)
	}
}

func TestNewAcceptsDomainBoundaries(t *testing.T) {
	t.Parallel()

	domains := make([]string, certmagic.MaxDomains)
	for i := range domains {
		domains[i] = "example.test"
	}
	domains[len(domains)-1] = strings.Join([]string{
		strings.Repeat("a", 63),
		strings.Repeat("b", 63),
		strings.Repeat("c", 63),
		strings.Repeat("d", 61),
	}, ".")
	manager, err := certmagic.New(certmagic.Options{
		Domains: domains,
		Storage: &cm.FileStorage{Path: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewNormalizesAndDeduplicatesDomains(t *testing.T) {
	t.Parallel()
	manager, err := certmagic.New(certmagic.Options{
		Domains: []string{"BÜCHER.example", "xn--bcher-kva.example", "EXAMPLE.test"},
		Storage: &cm.FileStorage{Path: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewBuildsLocalManagerWithoutMutatingCertMagicGlobals(t *testing.T) {
	t.Parallel()

	before := struct {
		ca                   string
		email                string
		agreed               bool
		disableHTTPChallenge bool
	}{
		ca:                   cm.DefaultACME.CA,
		email:                cm.DefaultACME.Email,
		agreed:               cm.DefaultACME.Agreed,
		disableHTTPChallenge: cm.DefaultACME.DisableHTTPChallenge,
	}

	manager, err := certmagic.New(certmagic.Options{
		Domains: []string{"example.test"},
		Email:   "ops@example.test",
		Staging: true,
		Storage: &cm.FileStorage{Path: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	got := manager.TLSConfig()
	if got == nil || got.GetCertificate == nil {
		t.Fatalf("TLSConfig = %#v", got)
	}
	if got.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2", got.MinVersion)
	}
	wantNextProtos := []string{"h2", "http/1.1", "acme-tls/1"}
	if !slices.Equal(got.NextProtos, wantNextProtos) {
		t.Errorf("NextProtos = %q, want %q", got.NextProtos, wantNextProtos)
	}
	after := struct {
		ca                   string
		email                string
		agreed               bool
		disableHTTPChallenge bool
	}{
		ca:                   cm.DefaultACME.CA,
		email:                cm.DefaultACME.Email,
		agreed:               cm.DefaultACME.Agreed,
		disableHTTPChallenge: cm.DefaultACME.DisableHTTPChallenge,
	}
	if after != before {
		t.Fatalf("certmagic.DefaultACME changed: before=%+v after=%+v", before, after)
	}
}
