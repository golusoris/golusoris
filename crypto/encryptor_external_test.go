package crypto_test

import (
	"bytes"
	"testing"

	"github.com/golusoris/golusoris/crypto"
)

func TestEncryptorRoundTrip(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x01}, 32)
	e, err := crypto.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	plain := []byte("field-level secret value")
	sealed, err := e.Seal(plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(sealed, plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	out, err := e.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(out, plain) {
		t.Errorf("round trip = %q, want %q", out, plain)
	}
}

func TestNewEncryptorRejectsBadKeyLength(t *testing.T) {
	t.Parallel()
	if _, err := crypto.NewEncryptor([]byte("too short")); err == nil {
		t.Fatal("want error for a 9-byte key")
	}
}

func TestSecureToken(t *testing.T) {
	t.Parallel()
	tok, err := crypto.SecureToken(16)
	if err != nil {
		t.Fatalf("SecureToken: %v", err)
	}
	if len(tok) != 32 { // hex doubles the byte count
		t.Errorf("len = %d, want 32 hex chars", len(tok))
	}
	other, _ := crypto.SecureToken(16)
	if tok == other {
		t.Error("two SecureToken calls returned the same value")
	}
}
