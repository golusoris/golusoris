package crypto_test

import (
	"bytes"
	"testing"

	"github.com/golusoris/golusoris/crypto"
)

func TestPasswordRoundtrip(t *testing.T) {
	t.Parallel()
	hash, err := crypto.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, rehash, err := crypto.VerifyPassword("hunter2", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("expected match=true")
	}
	if rehash {
		t.Error("default params should not need rehash")
	}
}

func TestPasswordWrong(t *testing.T) {
	t.Parallel()
	hash, _ := crypto.HashPassword("hunter2")
	ok, _, err := crypto.VerifyPassword("wrong", hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("expected match=false")
	}
}

func TestSealOpenRoundtrip(t *testing.T) {
	t.Parallel()
	key, err := crypto.RandomBytes(32)
	if err != nil {
		t.Fatal(err)
	}
	pt := []byte("the quick brown fox")
	ct, err := crypto.Seal(key, pt)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(ct, pt) {
		t.Error("ciphertext == plaintext")
	}
	out, err := crypto.Open(key, ct)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(out, pt) {
		t.Errorf("Open returned %q, want %q", out, pt)
	}
}

func TestOpenShortInput(t *testing.T) {
	t.Parallel()
	key, _ := crypto.RandomBytes(32)
	if _, err := crypto.Open(key, []byte{1, 2, 3}); err == nil {
		t.Error("expected error for short ciphertext")
	}
}

func TestHashPasswordWith(t *testing.T) {
	t.Parallel()
	h, err := crypto.HashPasswordWith("secret", crypto.DefaultPasswordParams)
	if err != nil {
		t.Fatalf("HashPasswordWith: %v", err)
	}
	ok, _, err := crypto.VerifyPassword("secret", h)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("expected match=true")
	}
}
