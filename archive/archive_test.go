package archive_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/golusoris/golusoris/archive"
)

func TestCreateAndExtract_zip(t *testing.T) {
	srcDir := t.TempDir()
	// Create some files to archive.
	if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "b.txt"), []byte("world"), 0o640); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "bundle.zip")
	if err := archive.Create(context.Background(), dest, []string{srcDir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Verify the zip was created.
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("archive not created: %v", err)
	}

	// Extract to a new dir.
	extractDir := t.TempDir()
	if err := archive.Extract(context.Background(), dest, extractDir); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// At least one entry should exist.
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one extracted entry")
	}
}

func TestExtract_notFound(t *testing.T) {
	err := archive.Extract(context.Background(), "/nonexistent/archive.zip", t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing archive")
	}
}
