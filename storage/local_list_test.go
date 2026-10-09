// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/golusoris/golusoris/storage"
)

// seedLocalKeys writes keys as plain files under dir, bypassing Put's
// per-object fsync so large fixtures stay fast.
func seedLocalKeys(t *testing.T, dir string, keys []string) {
	t.Helper()
	for _, key := range keys {
		name := filepath.Join(dir, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func newSeededLocalBucket(t *testing.T, keys []string) *storage.LocalBucket {
	t.Helper()
	dir := t.TempDir()
	seedLocalKeys(t, dir, keys)
	b, err := storage.NewLocalBucket(dir)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func listKeys(ctx context.Context, t *testing.T, b storage.Bucket, opts storage.ListOptions) []string {
	t.Helper()
	objects, err := b.List(ctx, opts)
	if err != nil {
		t.Fatalf("List(%+v): %v", opts, err)
	}
	keys := make([]string, 0, len(objects))
	for _, obj := range objects {
		keys = append(keys, obj.Key)
	}
	return keys
}

// byteOrderKeys sorts '-' (0x2d) and '.' (0x2e) before '/' (0x2f), so a
// directory's subtree sorts between its siblings, not before them.
var byteOrderKeys = []string{"a-x", "a.txt", "a/b", "a/c/d", "a/c/e", "a0", "b"}

func TestLocalBucket_ListReturnsByteOrder(t *testing.T) {
	t.Parallel()
	reversed := slices.Clone(byteOrderKeys)
	slices.Reverse(reversed)
	b := newSeededLocalBucket(t, reversed)
	if got := listKeys(t.Context(), t, b, storage.ListOptions{}); !slices.Equal(got, byteOrderKeys) {
		t.Fatalf("List = %v; want byte order %v", got, byteOrderKeys)
	}
	if got := listKeys(t.Context(), t, b, storage.ListOptions{Limit: 3}); !slices.Equal(got, byteOrderKeys[:3]) {
		t.Fatalf("List limit 3 = %v; want smallest keys %v", got, byteOrderKeys[:3])
	}
}

func TestLocalBucket_ListStartAfter(t *testing.T) {
	t.Parallel()
	b := newSeededLocalBucket(t, append(slices.Clone(byteOrderKeys), "c/1", "c/2", "c/3"))
	for _, tc := range []struct {
		name string
		opts storage.ListOptions
		want []string
	}{
		{name: "existing key", opts: storage.ListOptions{StartAfter: "a.txt"}, want: []string{"a/b", "a/c/d", "a/c/e", "a0", "b", "c/1", "c/2", "c/3"}},
		{name: "inside subtree", opts: storage.ListOptions{StartAfter: "a/c/d", Limit: 2}, want: []string{"a/c/e", "a0"}},
		{name: "between keys", opts: storage.ListOptions{StartAfter: "a/bz"}, want: []string{"a/c/d", "a/c/e", "a0", "b", "c/1", "c/2", "c/3"}},
		{name: "directory name", opts: storage.ListOptions{StartAfter: "a/c"}, want: []string{"a/c/d", "a/c/e", "a0", "b", "c/1", "c/2", "c/3"}},
		{name: "directory prefix", opts: storage.ListOptions{StartAfter: "a/c/"}, want: []string{"a/c/d", "a/c/e", "a0", "b", "c/1", "c/2", "c/3"}},
		{name: "last key", opts: storage.ListOptions{StartAfter: "c/3"}},
		{name: "after every key", opts: storage.ListOptions{StartAfter: "zz"}},
		{name: "with prefix", opts: storage.ListOptions{Prefix: "c/", StartAfter: "c/1"}, want: []string{"c/2", "c/3"}},
		{name: "before prefix range", opts: storage.ListOptions{Prefix: "c/", StartAfter: "a/c/e"}, want: []string{"c/1", "c/2", "c/3"}},
		{name: "after prefix range", opts: storage.ListOptions{Prefix: "a/", StartAfter: "b"}},
	} {
		if got := listKeys(t.Context(), t, b, tc.opts); !slices.Equal(got, tc.want) {
			t.Errorf("%s: List = %v; want %v", tc.name, got, tc.want)
		}
	}
	for _, after := range []string{"../x", "a//b", "/abs", "a/./b"} {
		if _, err := b.List(context.Background(), storage.ListOptions{StartAfter: after}); !errors.Is(err, storage.ErrUnsafeKey) {
			t.Errorf("List StartAfter %q error = %v; want ErrUnsafeKey", after, err)
		}
	}
}

func TestLocalBucket_WalkListsPastOnePage(t *testing.T) {
	t.Parallel()
	want := append(numberedKeys("flat/", storage.MaxListLimit+1), numberedKeys("nested/d/", 30)...)
	b := newSeededLocalBucket(t, want)
	got, err := walkKeys(context.Background(), b, storage.ListOptions{})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Walk returned %d keys; want %d in byte order", len(got), len(want))
	}
	got, err = walkKeys(context.Background(), b, storage.ListOptions{Prefix: "nested/", Limit: 7, StartAfter: "nested/d/0004"})
	if err != nil || !slices.Equal(got, want[storage.MaxListLimit+6:]) {
		t.Fatalf("Walk nested from 0004 = %d keys, %v; want %d", len(got), err, len(want)-storage.MaxListLimit-6)
	}
}
