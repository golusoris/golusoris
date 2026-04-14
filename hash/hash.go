// Package hash provides content-hashing helpers using SHA-256 (cryptographic),
// xxhash (fast non-cryptographic), and BLAKE3 (fast cryptographic).
//
// Picks: cespare/xxhash v2 (fastest 64-bit hash in Go, used by Prometheus),
// zeebo/blake3 (pure-Go BLAKE3, 3× faster than SHA-256 on large inputs).
//
// Usage:
//
//	sum := hash.SHA256File(path)
//	etag := hash.ETag(data)
//	fast := hash.XX64(data)
package hash

import (
	"crypto/sha1" //nolint:gosec // SHA-1 for ETag compatibility only
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"

	"github.com/cespare/xxhash/v2"
	"github.com/zeebo/blake3"
)

// SHA256 returns the hex-encoded SHA-256 digest of data.
func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SHA256Reader streams r and returns its hex-encoded SHA-256 digest.
func SHA256Reader(r io.Reader) (string, error) {
	return hexHash(sha256.New(), r)
}

// SHA256File opens path and returns its hex-encoded SHA-256 digest.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path validated by caller
	if err != nil {
		return "", fmt.Errorf("hash: open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	return SHA256Reader(f)
}

// BLAKE3 returns the hex-encoded BLAKE3 digest of data.
func BLAKE3(data []byte) string {
	sum := blake3.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// BLAKE3Reader streams r and returns its hex-encoded BLAKE3 digest.
func BLAKE3Reader(r io.Reader) (string, error) {
	h := blake3.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("hash: blake3 read: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// XX64 returns the xxhash-64 digest of data as a hex string.
func XX64(data []byte) string {
	return fmt.Sprintf("%016x", xxhash.Sum64(data))
}

// XX64Reader streams r through xxhash-64 and returns the hex digest.
func XX64Reader(r io.Reader) (string, error) {
	h := xxhash.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("hash: xx64 read: %w", err)
	}
	return fmt.Sprintf("%016x", h.Sum64()), nil
}

// ETag computes an HTTP ETag value for data using SHA-1 (RFC 7232 §2.3).
// The returned string includes the enclosing double-quotes.
func ETag(data []byte) string {
	h := sha1.New() //nolint:gosec // SHA-1 for ETag, not security-critical
	h.Write(data)
	return `"` + hex.EncodeToString(h.Sum(nil)) + `"`
}

// --- helpers ---

func hexHash(h hash.Hash, r io.Reader) (string, error) {
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("hash: read: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
