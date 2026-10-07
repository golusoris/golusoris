// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jwt_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jonboulle/clockwork"

	jwtpkg "github.com/golusoris/golusoris/auth/jwt"
)

type testClaims struct {
	jwt.RegisteredClaims
	UserID string `json:"uid"`
}

type nonComparableClock struct {
	clockwork.Clock
	state []byte
}

func newSigner(t *testing.T, secret string) *jwtpkg.Signer {
	t.Helper()
	s, err := jwtpkg.NewHMACSigner(jwtpkg.HS256, []byte(secret), time.Hour)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	return s
}

func TestNewHMACSigner_EmptySecret(t *testing.T) {
	t.Parallel()
	if _, err := jwtpkg.NewHMACSigner(jwtpkg.HS256, nil, time.Hour); err == nil {
		t.Fatal("expected error for empty secret")
	}
}

func TestNewHMACSignerRejectsNonHMAC(t *testing.T) {
	t.Parallel()
	if _, err := jwtpkg.NewHMACSigner(jwt.SigningMethodRS256, []byte("secret"), time.Hour); err == nil {
		t.Fatal("expected error for non-HMAC signing method")
	}
}

func TestNewHMACSignerRejectsTTLBelowNumericDatePrecision(t *testing.T) {
	t.Parallel()
	for _, ttl := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond, time.Second - 1} {
		if _, err := jwtpkg.NewHMACSigner(jwtpkg.HS256, []byte(strings.Repeat("s", 32)), ttl); err == nil {
			t.Errorf("NewHMACSigner TTL %v: expected error", ttl)
		}
	}
	if _, err := jwtpkg.NewHMACSigner(jwtpkg.HS256, []byte(strings.Repeat("s", 32)), time.Second); err != nil {
		t.Fatalf("NewHMACSigner one-second TTL: %v", err)
	}
}

func TestNewHMACSignerEnforcesAlgorithmKeySize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		alg  jwt.SigningMethod
		size int
	}{
		{name: "HS256", alg: jwtpkg.HS256, size: 32},
		{name: "HS384", alg: jwtpkg.HS384, size: 48},
		{name: "HS512", alg: jwtpkg.HS512, size: 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := jwtpkg.NewHMACSigner(tt.alg, []byte(strings.Repeat("s", tt.size-1)), time.Hour)
			if err == nil {
				t.Fatal("short key accepted")
			}
			_, err = jwtpkg.NewHMACSigner(tt.alg, []byte(strings.Repeat("s", tt.size)), time.Hour)
			if err != nil {
				t.Fatalf("exact-size key rejected: %v", err)
			}
		})
	}
}

func TestSignerRemainsComparable(t *testing.T) {
	t.Parallel()
	signer := newSigner(t, "test-secret-32-bytes-long-enough")

	set := map[jwtpkg.Signer]struct{}{*signer: {}}
	if _, ok := set[*signer]; !ok {
		t.Fatal("Signer value is not usable as a comparable key")
	}
}

func TestSignerRemainsComparableWithNonComparableClock(t *testing.T) {
	t.Parallel()

	clk := nonComparableClock{Clock: clockwork.NewFakeClock(), state: []byte("mutable")}
	signer, err := jwtpkg.NewHMACSignerWithClock(
		jwtpkg.HS256,
		[]byte("test-secret-32-bytes-long-enough"),
		time.Hour,
		clk,
	)
	if err != nil {
		t.Fatalf("NewHMACSignerWithClock: %v", err)
	}
	set := map[jwtpkg.Signer]struct{}{*signer: {}}
	if _, ok := set[*signer]; !ok {
		t.Fatal("Signer value is not usable as a comparable key")
	}
}

func TestSignAddsConfiguredExpiryWhenAbsent(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "test-secret-32-bytes-long-enough")

	tok, err := s.Sign(testClaims{UserID: "u-1"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var got testClaims
	if err := s.Parse(tok, &got); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.ExpiresAt == nil {
		t.Fatal("ExpiresAt = nil, want configured TTL applied")
	}
}

func TestSignRejectsNilClaims(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "test-secret-32-bytes-long-enough")
	_, err := s.Sign(nil)
	if err == nil {
		t.Fatal("nil claims accepted")
	}
	var claims *testClaims
	_, err = s.Sign(claims)
	if err == nil {
		t.Fatal("typed-nil claims accepted")
	}
}

func TestParseRejectsTokenWithoutExpiry(t *testing.T) {
	t.Parallel()
	secret := []byte("test-secret-32-bytes-long-enough")
	s := newSigner(t, string(secret))
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, testClaims{UserID: "u-1"}).SignedString(secret)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}

	err = s.Parse(tok, &testClaims{})
	if !errors.Is(err, jwt.ErrTokenRequiredClaimMissing) {
		t.Fatalf("Parse error = %v, want ErrTokenRequiredClaimMissing", err)
	}
	if !jwtpkg.ErrInvalid(err) {
		t.Fatalf("ErrInvalid(%v) = false", err)
	}
}

func TestParseRejectsNilClaims(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "test-secret-32-bytes-long-enough")
	tok, err := s.Sign(testClaims{UserID: "u-1"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if err := s.Parse(tok, nil); err == nil {
		t.Fatal("Parse accepted nil claims")
	}
	var claims *testClaims
	if err := s.Parse(tok, claims); err == nil {
		t.Fatal("Parse accepted typed-nil claims")
	}
}

func TestParseExpiresAtExactTTLBoundary(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(start)
	s, err := jwtpkg.NewHMACSignerWithClock(
		jwtpkg.HS256,
		[]byte("test-secret-32-bytes-long-enough"),
		time.Hour,
		clk,
	)
	if err != nil {
		t.Fatalf("NewHMACSignerWithClock: %v", err)
	}
	tok, err := s.Sign(testClaims{UserID: "u-1"})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if err := s.Parse(tok, &testClaims{}); err != nil {
		t.Fatalf("Parse before expiry: %v", err)
	}
	clk.Advance(time.Hour)
	if err := s.Parse(tok, &testClaims{}); !jwtpkg.ErrExpired(err) {
		t.Fatalf("Parse at expiry = %v, want expired", err)
	}
}

func TestSignAndParse(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "test-secret-32-bytes-long-enough")

	claims := testClaims{
		Subject:   "u-1",
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		UserID:    "u-1",
	}
	tok, err := s.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var got testClaims
	if err := s.Parse(tok, &got); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.UserID != "u-1" {
		t.Errorf("UserID = %q, want u-1", got.UserID)
	}
}

func TestParseExpired(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "test-secret-32-bytes-long-enough")

	claims := testClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
	}
	tok, _ := s.Sign(claims)
	err := s.Parse(tok, &testClaims{})
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if !jwtpkg.ErrExpired(err) {
		t.Errorf("ErrExpired = false, want true; err = %v", err)
	}
}

func TestParseWrongSecret(t *testing.T) {
	t.Parallel()
	signer := newSigner(t, "secret-a-32-bytes-long-enough-key")
	verifier := newSigner(t, "secret-b-32-bytes-long-enough-key")

	tok, _ := signer.Sign(testClaims{})
	if err := verifier.Parse(tok, &testClaims{}); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestErrInvalid_wrongSignature(t *testing.T) {
	t.Parallel()
	signer := newSigner(t, "secret-a-32-bytes-long-enough-key")
	verifier := newSigner(t, "secret-b-32-bytes-long-enough-key")

	tok, _ := signer.Sign(testClaims{})
	err := verifier.Parse(tok, &testClaims{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !jwtpkg.ErrInvalid(err) {
		t.Errorf("ErrInvalid = false, want true for wrong-signature error; err = %v", err)
	}
}

func TestErrInvalid_malformed(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "test-secret-32-bytes-long-enough")
	err := s.Parse("not.a.jwt", &testClaims{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !jwtpkg.ErrInvalid(err) {
		t.Errorf("ErrInvalid = false, want true for malformed token; err = %v", err)
	}
}

func TestErrInvalid_validToken(t *testing.T) {
	t.Parallel()
	s := newSigner(t, "test-secret-32-bytes-long-enough")
	claims := testClaims{
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tok, _ := s.Sign(claims)
	err := s.Parse(tok, &testClaims{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ErrInvalid must be false when there is no error.
	if jwtpkg.ErrInvalid(nil) {
		t.Error("ErrInvalid(nil) = true, want false")
	}
}
