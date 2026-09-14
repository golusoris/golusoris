// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oauth2server

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestRandomB64_length(t *testing.T) {
	t.Parallel()
	s, err := randomB64(16)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == "" {
		t.Error("expected non-empty string")
	}
}

func TestRandomB64_unique(t *testing.T) {
	t.Parallel()
	a, err := randomB64(16)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	b, err := randomB64(16)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if a == b {
		t.Error("expected two different random strings, got identical")
	}
}

func TestContains_found(t *testing.T) {
	t.Parallel()
	if !contains([]string{"a", "b"}, "b") {
		t.Error("expected true for found element")
	}
}

func TestContains_notFound(t *testing.T) {
	t.Parallel()
	if contains([]string{"a", "b"}, "c") {
		t.Error("expected false for missing element")
	}
}

func TestContains_empty(t *testing.T) {
	t.Parallel()
	if contains(nil, "x") {
		t.Error("expected false for nil haystack")
	}
}

func TestVerifyPKCE_emptyVerifier(t *testing.T) {
	t.Parallel()
	if verifyPKCE("challenge", "plain", "") {
		t.Error("expected false for empty verifier")
	}
}

func TestVerifyPKCE_plain(t *testing.T) {
	t.Parallel()
	verifier := "testverifier123"
	if !verifyPKCE(verifier, "plain", verifier) {
		t.Error("expected true for plain method with matching verifier")
	}
}

func TestVerifyPKCE_S256(t *testing.T) {
	t.Parallel()
	verifier := "testverifier123"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if !verifyPKCE(challenge, "S256", verifier) {
		t.Error("expected true for S256 method with correct challenge")
	}
}

func TestVerifyPKCE_unknown(t *testing.T) {
	t.Parallel()
	if verifyPKCE("challenge", "other", "verifier") {
		t.Error("expected false for unknown method")
	}
}

func TestPickRegistered_found(t *testing.T) {
	t.Parallel()
	if got := pickRegistered([]string{"a", "b"}, "b"); got != "b" {
		t.Errorf("expected registered entry %q, got %q", "b", got)
	}
}

func TestPickRegistered_notFound(t *testing.T) {
	t.Parallel()
	if got := pickRegistered([]string{"a", "b"}, "c"); got != "" {
		t.Errorf("expected empty string for unregistered candidate, got %q", got)
	}
}

func TestPickRegistered_prefixIsNotAMatch(t *testing.T) {
	t.Parallel()
	if got := pickRegistered([]string{"http://x/cb"}, "http://x/cb/"); got != "" {
		t.Errorf("expected empty string for a prefix-only match, got %q", got)
	}
}

func TestPickRegistered_emptyRegistered(t *testing.T) {
	t.Parallel()
	if got := pickRegistered(nil, "a"); got != "" {
		t.Errorf("expected empty string for nil registered list, got %q", got)
	}
}

func TestPickRegistered_emptyCandidate(t *testing.T) {
	t.Parallel()
	if got := pickRegistered([]string{"a"}, ""); got != "" {
		t.Errorf("expected empty string for empty candidate, got %q", got)
	}
}
