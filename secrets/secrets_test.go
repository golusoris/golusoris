// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secrets_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
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
	if _, ok := errors.AsType[secrets.ErrNotFound](err); !ok {
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

func TestFileRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-secret")
	if err := os.WriteFile(outside, []byte("must-not-leak"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	value, err := secrets.File(root).Get(t.Context(), "linked")
	if err == nil {
		t.Fatalf("symlink escape returned %q", value)
	}
}

func TestFileSizeBoundaries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	exact := bytes.Repeat([]byte{'x'}, int(secrets.MaxFileBytes))
	if err := os.WriteFile(filepath.Join(root, "exact"), exact, 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := secrets.File(root).Get(t.Context(), "exact")
	if err != nil {
		t.Fatalf("exact boundary: %v", err)
	}
	if len(value) != len(exact) {
		t.Fatalf("exact boundary length = %d, want %d", len(value), len(exact))
	}
	if err = os.WriteFile(filepath.Join(root, "large"), append(exact, 'x'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = secrets.File(root).Get(t.Context(), "large"); !errors.Is(err, secrets.ErrTooLarge) {
		t.Fatalf("oversized error = %v, want ErrTooLarge", err)
	}
}

func TestFileRejectsNonRegularAndCanceledReads(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := secrets.File(root)
	if _, err := store.Get(t.Context(), "directory"); err == nil {
		t.Fatal("directory accepted as secret")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.Get(ctx, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Get error = %v, want context.Canceled", err)
	}
}

func TestErrNotFoundMessage(t *testing.T) {
	t.Parallel()
	e := secrets.ErrNotFound{Key: "my-key"}
	if e.Error() == "" {
		t.Error("Error() returned empty string")
	}
	if !errors.As(e, &secrets.ErrNotFound{}) {
		t.Error("expected ErrNotFound to satisfy errors.As")
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
	if _, ok := errors.AsType[secrets.ErrNotFound](err); !ok {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStaticClonesCallerMap(t *testing.T) {
	t.Parallel()
	source := map[string]string{"api_key": "original"}
	store := secrets.Static(source)
	source["api_key"] = "mutated"
	value, err := store.Get(t.Context(), "api_key")
	if err != nil {
		t.Fatal(err)
	}
	if value != "original" {
		t.Fatalf("value = %q, want original", value)
	}
}

func TestStaticDoesNotRaceCallerMutation(t *testing.T) {
	t.Parallel()
	source := map[string]string{"api_key": "original"}
	store := secrets.Static(source)
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 10_000 {
			source["api_key"] = "mutated"
		}
	})
	for range 10_000 {
		value, err := store.Get(t.Context(), "api_key")
		if err != nil || value != "original" {
			t.Fatalf("Get = (%q, %v), want original", value, err)
		}
	}
	workers.Wait()
}
