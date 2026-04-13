package hash_test

import (
	"strings"
	"testing"

	"github.com/golusoris/golusoris/hash"
)

func TestSHA256(t *testing.T) {
	got := hash.SHA256([]byte("hello"))
	// known SHA-256 of "hello"
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Fatalf("SHA256: got %q want %q", got, want)
	}
}

func TestSHA256Reader(t *testing.T) {
	got, err := hash.SHA256Reader(strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Fatalf("SHA256Reader: got %q want %q", got, want)
	}
}

func TestBLAKE3(t *testing.T) {
	got := hash.BLAKE3([]byte("hello"))
	if len(got) != 64 { // 32 bytes = 64 hex chars
		t.Fatalf("BLAKE3: unexpected length %d", len(got))
	}
	// Verify consistency.
	if hash.BLAKE3([]byte("hello")) != got {
		t.Fatal("BLAKE3 not deterministic")
	}
}

func TestXX64(t *testing.T) {
	got := hash.XX64([]byte("hello"))
	if len(got) != 16 {
		t.Fatalf("XX64: unexpected length %d", len(got))
	}
	if hash.XX64([]byte("hello")) != got {
		t.Fatal("XX64 not deterministic")
	}
}

func TestETag(t *testing.T) {
	etag := hash.ETag([]byte("hello"))
	if len(etag) < 3 || etag[0] != '"' || etag[len(etag)-1] != '"' {
		t.Fatalf("ETag not quoted: %q", etag)
	}
}

func TestDifferentInputsDifferentHashes(t *testing.T) {
	if hash.SHA256([]byte("a")) == hash.SHA256([]byte("b")) {
		t.Fatal("SHA256 collision")
	}
	if hash.XX64([]byte("a")) == hash.XX64([]byte("b")) {
		t.Fatal("XX64 collision")
	}
	if hash.BLAKE3([]byte("a")) == hash.BLAKE3([]byte("b")) {
		t.Fatal("BLAKE3 collision")
	}
}
