package secrets_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/golusoris/golusoris/secrets"
)

func TestEnv_found(t *testing.T) {
	t.Setenv("TEST_SECRET_KEY", "hunter2")
	s := secrets.Env()
	v, err := s.Get(context.Background(), "TEST_SECRET_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "hunter2" {
		t.Fatalf("got %q, want %q", v, "hunter2")
	}
}

func TestEnv_notFound(t *testing.T) {
	t.Parallel()
	s := secrets.Env()
	_, err := s.Get(context.Background(), "GOLUSORIS_NONEXISTENT_XYZ")
	var nf secrets.ErrNotFound
	if !errors.As(err, &nf) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if nf.Key != "GOLUSORIS_NONEXISTENT_XYZ" {
		t.Fatalf("wrong key in error: %s", nf.Key)
	}
}

func TestFile_found(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "db_password"), []byte("  secret123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := secrets.File(dir)
	v, err := s.Get(context.Background(), "db_password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "secret123" {
		t.Fatalf("got %q, want %q", v, "secret123")
	}
}

func TestFile_notFound(t *testing.T) {
	t.Parallel()
	s := secrets.File(t.TempDir())
	_, err := s.Get(context.Background(), "missing_key")
	var nf secrets.ErrNotFound
	if !errors.As(err, &nf) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFile_pathTraversal(t *testing.T) {
	t.Parallel()
	s := secrets.File(t.TempDir())
	_, err := s.Get(context.Background(), "../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path-separator key")
	}
}

func TestStatic(t *testing.T) {
	t.Parallel()
	s := secrets.Static(map[string]string{"api_key": "abc"})
	v, err := s.Get(context.Background(), "api_key")
	if err != nil || v != "abc" {
		t.Fatalf("got (%q, %v)", v, err)
	}
	_, err = s.Get(context.Background(), "missing")
	var nf secrets.ErrNotFound
	if !errors.As(err, &nf) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
