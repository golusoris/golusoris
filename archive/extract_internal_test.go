// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package archive

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// errOpenFS wraps an fstest.MapFS but always fails Open, so ReadDir/Stat
// still work (used to obtain a real fs.DirEntry) while exercising
// extractFileEntry's open-error path.
type errOpenFS struct {
	fstest.MapFS
}

func (errOpenFS) Open(name string) (fs.File, error) {
	return nil, fmt.Errorf("boom: %s", name)
}

func dirEntry(t *testing.T, fsys fs.FS, name string) fs.DirEntry {
	t.Helper()
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == name {
			return e
		}
	}
	t.Fatalf("entry %q not found", name)
	return nil
}

// Positive: extractFileEntry opens and copies a file entry's content to dest.
func TestExtractFileEntry_Success(t *testing.T) {
	t.Parallel()
	mapFS := fstest.MapFS{"file.txt": &fstest.MapFile{Data: []byte("hello")}}
	_ = dirEntry(t, mapFS, "file.txt") // sanity: entry exists in the MapFS
	dest := filepath.Join(t.TempDir(), "file.txt")

	if err := extractFileEntry(mapFS, dest, "file.txt"); err != nil {
		t.Fatalf("extractFileEntry = %v, want nil", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("extracted content = %q, want %q", got, "hello")
	}
}

// Negative: a failure opening the archive entry is wrapped and returned.
func TestExtractFileEntry_OpenError(t *testing.T) {
	t.Parallel()
	mapFS := errOpenFS{fstest.MapFS{"file.txt": &fstest.MapFile{Data: []byte("hello")}}}
	dest := filepath.Join(t.TempDir(), "file.txt")

	err := extractFileEntry(mapFS, dest, "file.txt")
	if err == nil || !strings.Contains(err.Error(), "archive: open entry") {
		t.Fatalf("extractFileEntry open error = %v, want wrapped open-entry error", err)
	}
}

// Negative/boundary: extractFileEntry's create step fails when dest is an
// existing directory rather than a writable file path.
func TestExtractFileEntry_CreateError(t *testing.T) {
	t.Parallel()
	mapFS := fstest.MapFS{"file.txt": &fstest.MapFile{Data: []byte("hello")}}
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "is-a-dir")
	if err := os.Mkdir(dest, 0o750); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	err := extractFileEntry(mapFS, dest, "file.txt")
	if err == nil || !strings.Contains(err.Error(), "archive: create") {
		t.Fatalf("extractFileEntry(dest=dir) = %v, want wrapped create error", err)
	}
}
