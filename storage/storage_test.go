// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/storage"
)

func TestLocalBucket(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Put
	obj, err := b.Put(ctx, "dir/file.txt", strings.NewReader("hello"), storage.PutOptions{})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if obj.Key != "dir/file.txt" {
		t.Fatalf("unexpected key: %q", obj.Key)
	}

	// Exists
	ok, err := b.Exists(ctx, "dir/file.txt")
	if err != nil || !ok {
		t.Fatalf("Exists: ok=%v err=%v", ok, err)
	}

	// Get
	rc, got, err := b.Get(ctx, "dir/file.txt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Size != 5 {
		t.Fatalf("size: expected 5, got %d", got.Size)
	}
	_ = rc.Close() // Windows refuses to delete a file that is still open

	// List
	objects, err := b.List(ctx, storage.ListOptions{Prefix: "dir/"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}

	// Stat
	stat, err := b.Stat(ctx, "dir/file.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.Key != "dir/file.txt" {
		t.Fatalf("Stat key: %q", stat.Key)
	}
	if stat.Size != 5 {
		t.Fatalf("Stat size: expected 5, got %d", stat.Size)
	}
	if stat.LastModified.IsZero() {
		t.Fatal("Stat LastModified is zero")
	}

	// Delete
	if err := b.Delete(ctx, "dir/file.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	ok, _ = b.Exists(ctx, "dir/file.txt")
	if ok {
		t.Fatal("expected file to be deleted")
	}

	// Delete non-existent — should not error
	if err := b.Delete(ctx, "does-not-exist.txt"); err != nil {
		t.Fatalf("Delete non-existent: %v", err)
	}
}

func TestLocalBucket_pathTraversal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, _ := storage.NewLocalBucket(dir)

	_, err := b.Put(context.Background(), "../evil.txt", strings.NewReader("x"), storage.PutOptions{})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestLocalBucket_notFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, _ := storage.NewLocalBucket(dir)

	_, _, err := b.Get(context.Background(), "missing.txt")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLocalBucket_StatNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, _ := storage.NewLocalBucket(dir)

	_, err := b.Stat(context.Background(), "missing.txt")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestLocalBucket_abs_nestedKeyRoundTrip is the positive case for the
// path-traversal guard behind abs(): a legitimate nested key must resolve
// under base, create the file at the expected nested location, and round
// trip through Put/Get unchanged.
func TestLocalBucket_abs_nestedKeyRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	const key = "a/b/c.txt"
	const content = "nested-content"

	if _, putErr := b.Put(ctx, key, strings.NewReader(content), storage.PutOptions{}); putErr != nil {
		t.Fatalf("Put: %v", putErr)
	}

	// The key must resolve to a real file physically nested under base,
	// mirroring the key's own directory structure.
	wantPath := filepath.Join(dir, "a", "b", "c.txt")
	if _, statErr := os.Stat(wantPath); statErr != nil {
		t.Fatalf("expected file at %s: %v", wantPath, statErr)
	}

	rc, obj, err := b.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != content {
		t.Fatalf("round-trip content mismatch: got %q, want %q", got, content)
	}
	if obj.Key != key {
		t.Fatalf("obj.Key = %q, want %q", obj.Key, key)
	}
}

// TestLocalBucket_abs_boundaryDotAndEmptyKey pins the CURRENT boundary
// behaviour of abs() for "." and "" — it does not change it. For both keys,
// filepath.Join(base, key) cleans to base itself, and abs()'s own guard
// (`clean != b.base`) explicitly treats that as acceptable: abs() does NOT
// return the path-traversal error for either key. Put still ends up
// failing, but only because the resolved path is a directory (base itself),
// which OpenFile refuses — a distinct failure from the traversal guard. The
// assertions below pin exactly that: an error occurs, but it is never the
// "path traversal attempt" error.
func TestLocalBucket_abs_boundaryDotAndEmptyKey(t *testing.T) {
	t.Parallel()

	for _, key := range []string{".", ""} {
		t.Run("key="+key, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			b, err := storage.NewLocalBucket(dir)
			if err != nil {
				t.Fatal(err)
			}

			_, putErr := b.Put(context.Background(), key, strings.NewReader("x"), storage.PutOptions{})
			if putErr == nil {
				t.Fatalf("Put(%q): expected an error (base is a directory), got nil", key)
			}
			if strings.Contains(putErr.Error(), "path traversal attempt") {
				t.Fatalf("Put(%q): abs() must accept this key (resolves to base), not reject it as traversal: %v", key, putErr)
			}
		})
	}
}

// TestLocalBucket_abs_negativeTraversalVariants covers keys that attempt to
// escape base. It proves the two genuinely rejected variants never open or
// create a file, and it separately pins the CURRENT (unchanged) behaviour
// for an absolute-looking key: filepath.Join(base, "/etc/passwd") does not
// treat the leading slash as an absolute-path override — it Cleans the
// concatenation of base+"/etc/passwd" into <base>/etc/passwd, which passes
// abs()'s own prefix check. That key is therefore accepted, not rejected —
// but it stays confined under base and never reaches the real /etc/passwd.
// This test documents that actual behaviour rather than asserting the
// (incorrect) assumption that abs() rejects it.
func TestLocalBucket_abs_negativeTraversalVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{name: "multi-level dotdot escapes above base", key: "../../x", wantErr: true},
		{name: "mid-path dotdot escapes above base", key: "a/../../x", wantErr: true},
		{name: "absolute-looking key stays confined under base", key: "/etc/passwd", wantErr: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			b, err := storage.NewLocalBucket(dir)
			if err != nil {
				t.Fatal(err)
			}

			_, putErr := b.Put(context.Background(), tc.key, strings.NewReader("x"), storage.PutOptions{})
			switch {
			case tc.wantErr && putErr == nil:
				t.Fatalf("Put(%q): expected a path-traversal rejection, got nil", tc.key)
			case tc.wantErr && !strings.Contains(putErr.Error(), "path traversal attempt"):
				t.Fatalf("Put(%q): expected the path-traversal error, got: %v", tc.key, putErr)
			case !tc.wantErr && putErr != nil:
				t.Fatalf("Put(%q): expected no error (confined under base), got: %v", tc.key, putErr)
			}

			// No file may ever land outside base. t.TempDir() gives each
			// (sub)test its own uniquely named parent directory, so
			// scanning dir's own parent is a reliable, isolated check even
			// under t.Parallel().
			parent := filepath.Dir(dir)
			entries, readErr := os.ReadDir(parent)
			if readErr != nil {
				t.Fatalf("ReadDir(parent): %v", readErr)
			}
			for _, e := range entries {
				if e.Name() != filepath.Base(dir) {
					t.Fatalf("Put(%q) leaked entry %q outside base", tc.key, e.Name())
				}
			}

			// For the rejected keys specifically: abs() must fail before
			// Put ever calls MkdirAll/OpenFile, so base itself must remain
			// empty.
			if tc.wantErr {
				baseEntries, baseReadErr := os.ReadDir(dir)
				if baseReadErr != nil {
					t.Fatalf("ReadDir(base): %v", baseReadErr)
				}
				if len(baseEntries) != 0 {
					t.Fatalf("Put(%q): expected no file created under base, found %d entries", tc.key, len(baseEntries))
				}
			}
		})
	}
}

// TestLocalBucket_abs_symlinkInsideBaseFollowsToOutsideTarget documents
// current, unchanged behaviour: abs() performs a purely LEXICAL check on the
// requested key (filepath.Join plus a string-prefix comparison) — it never
// calls filepath.EvalSymlinks or otherwise inspects what a path component
// resolves to on disk. A symlink that lives inside base but points outside
// it therefore passes abs() unchanged (the key is lexically under base),
// and Get's underlying os.Open then follows that symlink at the OS level,
// actually reading content from outside base.
//
// storage/AGENTS.md documents LocalBucket only as "path-traversal
// protected" and says nothing about symlinks, so this symlink-follow
// behaviour is an undocumented gap in the bucket's stated threat model, not
// a violation of a documented guarantee. This test pins what the code does
// today; it is not an endorsement that this is safe.
func TestLocalBucket_abs_symlinkInsideBaseFollowsToOutsideTarget(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	outside := t.TempDir()

	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("outside-content"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	linkPath := filepath.Join(base, "link")
	if err := os.Symlink(secret, linkPath); err != nil {
		t.Skipf("symlink not supported on this platform/filesystem: %v", err)
	}

	b, err := storage.NewLocalBucket(base)
	if err != nil {
		t.Fatal(err)
	}

	rc, _, err := b.Get(context.Background(), "link")
	if err != nil {
		t.Fatalf("Get(link): %v", err)
	}
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "outside-content" {
		t.Fatalf("expected Get to follow the symlink to outside content, got %q", got)
	}
}
