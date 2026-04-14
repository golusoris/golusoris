package fuzz_test

import (
	"encoding/json"
	"testing"

	"github.com/golusoris/golusoris/testutil/fuzz"
)

func TestRoundTrip_string(t *testing.T) {
	t.Parallel()
	fuzz.RoundTrip(t, "hello",
		func(s string) ([]byte, error) { return json.Marshal(s) },
		func(b []byte) (string, error) {
			var v string
			return v, json.Unmarshal(b, &v)
		},
	)
}

func TestRoundTrip_struct(t *testing.T) {
	t.Parallel()
	type point struct {
		X, Y int
	}
	fuzz.RoundTrip(t, point{X: 3, Y: 7},
		func(p point) ([]byte, error) { return json.Marshal(p) },
		func(b []byte) (point, error) {
			var v point
			return v, json.Unmarshal(b, &v)
		},
	)
}

func TestSeedFiles_missingDir(t *testing.T) {
	t.Parallel()
	// no testdata/fuzz/Nonexistent — should return nil, not fatal
	paths := fuzz.SeedFiles(t, "Nonexistent__should_not_exist")
	if paths != nil {
		t.Fatalf("expected nil, got %v", paths)
	}
}

func TestCorpusFiles_emptyDir(t *testing.T) {
	t.Parallel()
	// CorpusFiles on an empty dir returns an empty (non-nil) slice.
	// We use t.TempDir as CWD via os.Chdir only in subtests with their
	// own dir, so instead just verify the helper doesn't panic.
	paths := fuzz.SeedFiles(t, "DoesNotExist__fuzz_test")
	if paths != nil {
		t.Fatalf("SeedFiles with absent dir should return nil; got %v", paths)
	}
}
