// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/storage"
)

// checkPagedListing seeds MaxListLimit+1 keys under walk/ plus walk-other/x,
// which sorts before them ('-' < '/'), then checks Walk and StartAfter bounds.
func checkPagedListing(ctx context.Context, t *testing.T, b storage.Bucket) {
	t.Helper()
	want := numberedKeys("walk/", storage.MaxListLimit+1)
	for _, key := range append(slices.Clone(want), "walk-other/x") {
		if _, err := b.Put(ctx, key, strings.NewReader("x"), storage.PutOptions{}); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
	}
	got, err := walkKeys(ctx, b, storage.ListOptions{Prefix: "walk/"})
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Walk = %d keys, %v; want %d in order", len(got), err, len(want))
	}
	for _, tc := range []struct {
		name string
		opts storage.ListOptions
		want []string
	}{
		{name: "last key", opts: storage.ListOptions{Prefix: "walk/", StartAfter: want[len(want)-1]}},
		{name: "existing key", opts: storage.ListOptions{Prefix: "walk/", StartAfter: "walk/0499", Limit: 2}, want: want[500:502]},
		{name: "between keys", opts: storage.ListOptions{Prefix: "walk/", StartAfter: "walk/0499x", Limit: 2}, want: want[500:502]},
		{name: "before prefix range", opts: storage.ListOptions{Prefix: "walk/", StartAfter: "walk-other/x", Limit: 2}, want: want[:2]},
		{name: "no prefix", opts: storage.ListOptions{StartAfter: "walk-other/x", Limit: 2}, want: want[:2]},
		{name: "after prefix range", opts: storage.ListOptions{Prefix: "walk-other/", StartAfter: "walk/0000"}},
	} {
		if keys := listKeys(ctx, t, b, tc.opts); !slices.Equal(keys, tc.want) {
			t.Errorf("%s: List = %v; want %v", tc.name, keys, tc.want)
		}
	}
	if _, err = b.List(ctx, storage.ListOptions{StartAfter: "walk/../x"}); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Errorf("List invalid StartAfter = %v; want ErrUnsafeKey", err)
	}
}

func TestS3Bucket_PagedListing(t *testing.T) {
	t.Parallel()
	b := startS3Bucket(t, storage.S3Options{})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	checkPagedListing(ctx, t, b)
}

func TestLocalBucket_PagedListing(t *testing.T) {
	t.Parallel()
	b, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkPagedListing(t.Context(), t, b)
}
