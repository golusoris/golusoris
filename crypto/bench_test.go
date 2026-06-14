package crypto_test

import (
	"crypto/rand"
	"testing"

	"github.com/golusoris/golusoris/crypto"
)

// BenchmarkHashPassword measures the argon2id cost (intentionally expensive —
// this is the password-hardening hot path, run once per login).
func BenchmarkHashPassword(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := crypto.HashPassword("correct horse battery staple"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSealOpen measures an AES-256-GCM seal+open round trip (the
// column/field-encryption hot path).
func BenchmarkSealOpen(b *testing.B) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		b.Fatal(err)
	}
	pt := []byte("the quick brown fox jumps over the lazy dog")
	b.ReportAllocs()
	for b.Loop() {
		sealed, err := crypto.Seal(key, pt)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := crypto.Open(key, sealed); err != nil {
			b.Fatal(err)
		}
	}
}
