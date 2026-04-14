// Package fuzz provides helpers for fuzz testing.
//
// It assists with corpus management and round-trip validation — common
// patterns in parser/codec fuzz tests.
//
// Usage:
//
//	// Replay every seed in testdata/corpus/FuzzDecode:
//	for _, f := range fuzz.CorpusFiles(t, "FuzzDecode") {
//	    data, _ := os.ReadFile(f)
//	    _ = FuzzDecode(nil, data) // replay seed
//	}
//
//	// Assert encode → decode reproduces the original value:
//	fuzz.RoundTrip(t, input, encode, decode)
package fuzz

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// CorpusDir returns the path to testdata/corpus/<target>, creating it when absent.
func CorpusDir(t *testing.T, target string) string {
	t.Helper()
	dir := filepath.Join("testdata", "corpus", target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("fuzz: mkdir %s: %v", dir, err)
	}
	return dir
}

// CorpusFiles returns the paths of all non-directory entries under
// testdata/corpus/<target>. Creates the directory when absent.
func CorpusFiles(t *testing.T, target string) []string {
	t.Helper()
	dir := CorpusDir(t, target)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("fuzz: read %s: %v", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// SeedFiles returns the paths of all non-directory entries under
// testdata/fuzz/<target> (the standard Go toolchain seed corpus
// directory). Returns nil when the directory is absent.
func SeedFiles(t *testing.T, target string) []string {
	t.Helper()
	dir := filepath.Join("testdata", "fuzz", target)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("fuzz: read %s: %v", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// RoundTrip asserts that encode(v) followed by decode reproduces v via
// reflect.DeepEqual. Useful as a unit regression for serialisation fuzz targets.
func RoundTrip[T any](
	t *testing.T,
	v T,
	encode func(T) ([]byte, error),
	decode func([]byte) (T, error),
) {
	t.Helper()
	b, err := encode(v)
	if err != nil {
		t.Fatalf("fuzz: encode: %v", err)
	}
	got, err := decode(b)
	if err != nil {
		t.Fatalf("fuzz: decode: %v", err)
	}
	if !reflect.DeepEqual(v, got) {
		t.Fatalf("fuzz: round-trip mismatch\noriginal: %v\n decoded: %v", v, got)
	}
}
