package jwt_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	jwtpkg "github.com/golusoris/golusoris/auth/jwt"
)

type testClaims struct {
	jwt.RegisteredClaims
	UserID string `json:"uid"`
}

func TestSignAndParse(t *testing.T) {
	t.Parallel()
	s := jwtpkg.NewHMACSigner(jwtpkg.HS256, []byte("test-secret-32-bytes-long-enough"), time.Hour)

	claims := testClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "u-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		UserID: "u-1",
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
	s := jwtpkg.NewHMACSigner(jwtpkg.HS256, []byte("test-secret-32-bytes-long-enough"), time.Hour)

	claims := testClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
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
	signer := jwtpkg.NewHMACSigner(jwtpkg.HS256, []byte("secret-a"), time.Hour)
	verifier := jwtpkg.NewHMACSigner(jwtpkg.HS256, []byte("secret-b"), time.Hour)

	tok, _ := signer.Sign(testClaims{})
	if err := verifier.Parse(tok, &testClaims{}); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}
