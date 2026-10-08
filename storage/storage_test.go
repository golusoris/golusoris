// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/golusoris/golusoris/storage"
)

func TestLocalBucketRemainsComparable(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	set := map[storage.LocalBucket]struct{}{*bucket: {}}
	if _, ok := set[*bucket]; !ok {
		t.Fatal("LocalBucket value is not usable as a comparable key")
	}
}

func TestLocalBucket_ListFiniteDefaultAndLimitBoundaries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range storage.DefaultListLimit + 1 {
		name := filepath.Join(dir, fmt.Sprintf("%04d", i))
		if writeErr := os.WriteFile(name, []byte("x"), 0o600); writeErr != nil {
			t.Fatalf("seed object %d: %v", i, writeErr)
		}
	}

	objects, err := b.List(context.Background(), storage.ListOptions{})
	if err != nil {
		t.Fatalf("List default: %v", err)
	}
	if len(objects) != storage.DefaultListLimit {
		t.Fatalf("List default returned %d objects; want %d", len(objects), storage.DefaultListLimit)
	}

	objects, err = b.List(context.Background(), storage.ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List limit one: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("List limit one returned %d objects", len(objects))
	}

	for _, limit := range []int{-1, storage.MaxListLimit + 1} {
		if _, listErr := b.List(context.Background(), storage.ListOptions{Limit: limit}); listErr == nil {
			t.Fatalf("List limit %d: expected validation error", limit)
		}
	}
}

func TestLocalBucket_ListHonorsCancellation(t *testing.T) {
	t.Parallel()
	b, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = b.List(ctx, storage.ListOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("List canceled error = %v; want context.Canceled", err)
	}
}

func TestLocalBucket_ListHidesStagedObjects(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "nested"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "nested", ".golusoris-put-interrupted.tmp"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "nested", "stable"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}

	objects, err := b.List(context.Background(), storage.ListOptions{Prefix: "nested/"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objects) != 1 || objects[0].Key != "nested/stable" {
		t.Fatalf("List returned %+v; want only nested/stable", objects)
	}
}

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

func TestLocalBucket_PutOptionsPersistAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{"owner": "alice", "trace": "42"}
	wantMetadata := map[string]string{"owner": "alice", "trace": "42"}
	obj, err := b.Put(context.Background(), "object", strings.NewReader("body"), storage.PutOptions{
		ContentType: "text/plain",
		Metadata:    metadata,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	metadata["owner"] = "mutated"
	if obj.ContentType != "text/plain" || !reflect.DeepEqual(obj.Metadata, wantMetadata) {
		t.Fatalf("Put object = %+v; want persisted options", obj)
	}

	reopened, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}
	rc, got, err := reopened.Get(context.Background(), "object")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err = rc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got.ContentType != "text/plain" || !reflect.DeepEqual(got.Metadata, wantMetadata) {
		t.Fatalf("Get object = %+v; want persisted options", got)
	}
	got.Metadata["owner"] = "returned-map-mutation"
	stat, err := reopened.Stat(context.Background(), "object")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.ContentType != "text/plain" || !reflect.DeepEqual(stat.Metadata, wantMetadata) {
		t.Fatalf("Stat object = %+v; want isolated persisted options", stat)
	}
}

func TestLocalBucket_FailedReplacementPreservesMetadata(t *testing.T) {
	t.Parallel()
	b, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = b.Put(ctx, "object", strings.NewReader("old"), storage.PutOptions{
		ContentType: "text/old",
		Metadata:    map[string]string{"generation": "old"},
	}); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	src := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(errors.New("injected read failure")))
	if _, err = b.Put(ctx, "object", src, storage.PutOptions{
		ContentType: "text/new",
		Metadata:    map[string]string{"generation": "new"},
	}); err == nil {
		t.Fatal("replacement Put: expected error")
	}
	obj, err := b.Stat(ctx, "object")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if obj.ContentType != "text/old" || !reflect.DeepEqual(obj.Metadata, map[string]string{"generation": "old"}) {
		t.Fatalf("failed replacement metadata = %+v", obj)
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

func TestLocalBucket_failedReplacementPreservesObject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = b.Put(ctx, "dir/file.txt", strings.NewReader("original"), storage.PutOptions{}); err != nil {
		t.Fatalf("initial Put: %v", err)
	}

	src := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(errors.New("injected read failure")))
	if _, err = b.Put(ctx, "dir/file.txt", src, storage.PutOptions{}); err == nil {
		t.Fatal("replacement Put: expected error")
	}
	assertLocalObject(t, b, "dir/file.txt", "original")
	assertDirectoryNames(t, filepath.Join(dir, "dir"), "file.txt")
}

func TestLocalBucket_cancelledReplacementPreservesObject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Put(context.Background(), "file.txt", strings.NewReader("original"), storage.PutOptions{}); err != nil {
		t.Fatalf("initial Put: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = b.Put(ctx, "file.txt", strings.NewReader("replacement"), storage.PutOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Put error = %v; want context.Canceled", err)
	}
	assertLocalObject(t, b, "file.txt", "original")
	assertDirectoryNames(t, dir, "file.txt")
}

func TestLocalBucket_PreCanceledOperationsHaveNoSideEffects(t *testing.T) {
	t.Parallel()
	b, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Put(context.Background(), "object", strings.NewReader("stable"), storage.PutOptions{}); err != nil {
		t.Fatalf("seed Put: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rc, _, getErr := b.Get(ctx, "object")
	if rc != nil {
		_ = rc.Close()
		t.Fatal("pre-canceled Get returned reader")
	}
	if !errors.Is(getErr, context.Canceled) {
		t.Fatalf("Get error = %v; want context.Canceled", getErr)
	}
	if exists, existsErr := b.Exists(ctx, "object"); exists || !errors.Is(existsErr, context.Canceled) {
		t.Fatalf("Exists = %t, %v; want false, context.Canceled", exists, existsErr)
	}
	if _, statErr := b.Stat(ctx, "object"); !errors.Is(statErr, context.Canceled) {
		t.Fatalf("Stat error = %v; want context.Canceled", statErr)
	}
	if _, urlErr := b.URL(ctx, "object"); !errors.Is(urlErr, context.Canceled) {
		t.Fatalf("URL error = %v; want context.Canceled", urlErr)
	}
	if deleteErr := b.Delete(ctx, "object"); !errors.Is(deleteErr, context.Canceled) {
		t.Fatalf("Delete error = %v; want context.Canceled", deleteErr)
	}
	assertLocalObject(t, b, "object", "stable")
}

func TestLocalBucket_URLescapesReservedCharacters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}
	const key = "dir/a b#c.txt"
	if _, err = b.Put(context.Background(), key, strings.NewReader("x"), storage.PutOptions{}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	value, err := b.URL(context.Background(), key)
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	wantPath := filepath.ToSlash(filepath.Join(dir, filepath.FromSlash(key)))
	if !strings.HasPrefix(wantPath, "/") {
		wantPath = "/" + wantPath
	}
	if parsed.Scheme != "file" || parsed.Host != "" || parsed.Path != wantPath {
		t.Fatalf("URL = %q (scheme=%q host=%q path=%q); want file URL path %q", value, parsed.Scheme, parsed.Host, parsed.Path, wantPath)
	}
	if parsed.Fragment != "" || parsed.RawQuery != "" {
		t.Fatalf("URL reserved characters leaked into fragment/query: %q", value)
	}
}

func assertLocalObject(t *testing.T, b *storage.LocalBucket, key, want string) {
	t.Helper()
	rc, _, err := b.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			t.Errorf("close %q: %v", key, closeErr)
		}
	}()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("object %q = %q; want %q", key, got, want)
	}
}

func assertDirectoryNames(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	visible := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == ".golusoris-lock" || name == ".golusoris-staging" ||
			(strings.HasPrefix(name, ".golusoris-meta-") && strings.HasSuffix(name, ".json")) {
			continue
		}
		visible = append(visible, name)
	}
	if len(visible) != len(want) {
		t.Fatalf("ReadDir(%q) visible names = %v; want %v", dir, visible, want)
	}
	for i := range want {
		if visible[i] != want[i] {
			t.Fatalf("ReadDir(%q)[%d] = %q; want %q", dir, i, visible[i], want[i])
		}
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

// TestLocalBucket_nestedKeyRoundTrip covers a normal key through os.Root.
func TestLocalBucket_nestedKeyRoundTrip(t *testing.T) {
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

func TestLocalBucket_rejectsRootKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []string{".", ""} {
		t.Run("key="+key, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			b, err := storage.NewLocalBucket(dir)
			if err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			if _, putErr := b.Put(ctx, key, strings.NewReader("x"), storage.PutOptions{}); putErr == nil {
				t.Fatalf("Put(%q): expected unsafe-key error", key)
			}
			if _, _, getErr := b.Get(ctx, key); getErr == nil {
				t.Fatalf("Get(%q): expected unsafe-key error", key)
			}
			if deleteErr := b.Delete(ctx, key); deleteErr == nil {
				t.Fatalf("Delete(%q): expected unsafe-key error", key)
			}
			if _, existsErr := b.Exists(ctx, key); existsErr == nil {
				t.Fatalf("Exists(%q): expected unsafe-key error", key)
			}
			if _, statErr := b.Stat(ctx, key); statErr == nil {
				t.Fatalf("Stat(%q): expected unsafe-key error", key)
			}
			if _, urlErr := b.URL(ctx, key); urlErr == nil {
				t.Fatalf("URL(%q): expected unsafe-key error", key)
			}
		})
	}
}

func TestLocalBucket_rejectsTraversalVariants(t *testing.T) {
	t.Parallel()

	tests := []string{
		"../../x",
		"a/../../x",
		"/etc/passwd",
	}

	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			b, err := storage.NewLocalBucket(dir)
			if err != nil {
				t.Fatal(err)
			}

			if _, putErr := b.Put(context.Background(), key, strings.NewReader("x"), storage.PutOptions{}); putErr == nil {
				t.Fatalf("Put(%q): expected unsafe-key error", key)
			}

			parent := filepath.Dir(dir)
			entries, readErr := os.ReadDir(parent)
			if readErr != nil {
				t.Fatalf("ReadDir(parent): %v", readErr)
			}
			for _, e := range entries {
				if e.Name() != filepath.Base(dir) {
					t.Fatalf("Put(%q) leaked entry %q outside base", key, e.Name())
				}
			}

			baseEntries, baseReadErr := os.ReadDir(dir)
			if baseReadErr != nil {
				t.Fatalf("ReadDir(base): %v", baseReadErr)
			}
			if len(baseEntries) != 0 {
				t.Fatalf("Put(%q): expected no file created under base, found %d entries", key, len(baseEntries))
			}
		})
	}
}

func TestLocalBucket_blocksSymlinkEscape(t *testing.T) {
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

	ctx := context.Background()
	if _, _, getErr := b.Get(ctx, "link"); getErr == nil {
		t.Fatal("Get(link): expected symlink escape error")
	}
	if _, statErr := b.Stat(ctx, "link"); statErr == nil {
		t.Fatal("Stat(link): expected symlink escape error")
	}
	if _, existsErr := b.Exists(ctx, "link"); existsErr == nil {
		t.Fatal("Exists(link): expected symlink escape error")
	}
	if _, urlErr := b.URL(ctx, "link"); urlErr == nil {
		t.Fatal("URL(link): expected symlink escape error")
	}

	outsideDir := filepath.Join(outside, "dir")
	if err = os.Mkdir(outsideDir, 0o750); err != nil {
		t.Fatalf("Mkdir outside: %v", err)
	}
	if err = os.Symlink(outsideDir, filepath.Join(base, "dir-link")); err != nil {
		t.Skipf("directory symlink not supported: %v", err)
	}
	if _, putErr := b.Put(ctx, "dir-link/escaped.txt", strings.NewReader("x"), storage.PutOptions{}); putErr == nil {
		t.Fatal("Put through directory symlink: expected escape error")
	}
	if _, statErr := os.Stat(filepath.Join(outsideDir, "escaped.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("outside file state = %v; want not exist", statErr)
	}
}
