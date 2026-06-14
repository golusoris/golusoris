package archive_test

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/golusoris/golusoris/archive"
)

// buildZipBytes builds an in-memory zip whose entries are exactly the given
// name→content map. Names are written verbatim (CreateHeader) so traversal
// names like "../escape" survive — used to exercise the zip-slip defense.
func buildZipBytes(entries map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			continue
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

// writeZip writes buildZipBytes to a fresh temp .zip and returns its path.
func writeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.zip")
	if err := os.WriteFile(path, buildZipBytes(entries), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// escaped reports whether anything other than the destDir subtree was created
// under parent (i.e. an extraction escaped destDir).
func escaped(t *testing.T, parent, destName string) bool {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != destName {
			return true
		}
	}
	return false
}

// TestExtractRejectsZipSlip asserts a path-traversal entry cannot write outside
// destDir. destDir is nested under a parent temp dir so an escape lands a
// detectable sibling.
func TestExtractRejectsZipSlip(t *testing.T) {
	t.Parallel()
	src := writeZip(t, map[string]string{"../escape.txt": "pwned"})

	parent := t.TempDir()
	destDir := filepath.Join(parent, "extract")

	// Extract may legitimately reject the entry with an error; what must NOT
	// happen is a file materialising outside destDir.
	_ = archive.Extract(context.Background(), src, destDir)

	if escaped(t, parent, "extract") {
		t.Fatalf("zip-slip: an entry escaped destDir %s", destDir)
	}
}

// FuzzExtract mutates zip bytes and asserts Extract never panics and never
// writes outside destDir, whatever the (possibly corrupt) input.
func FuzzExtract(f *testing.F) {
	f.Add(buildZipBytes(map[string]string{"a.txt": "hello"}))
	f.Add(buildZipBytes(map[string]string{"../escape.txt": "x", "ok.txt": "y"}))
	f.Add(buildZipBytes(map[string]string{"sub/../../escape": "x"}))
	f.Add([]byte("PK\x03\x04 not really a zip"))

	f.Fuzz(func(t *testing.T, data []byte) {
		src := filepath.Join(t.TempDir(), "fuzz.zip")
		if err := os.WriteFile(src, data, 0o600); err != nil {
			t.Skip()
		}
		parent := t.TempDir()
		destDir := filepath.Join(parent, "extract")

		_ = archive.Extract(context.Background(), src, destDir) // must not panic

		if escaped(t, parent, "extract") {
			t.Fatalf("zip-slip: extraction escaped destDir for input %q", data)
		}
	})
}
