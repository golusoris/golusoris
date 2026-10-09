// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcs_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/storage"
	"github.com/golusoris/golusoris/storage/gcs"
)

func listGCSKeys(ctx context.Context, t *testing.T, b storage.Bucket, opts storage.ListOptions) []string {
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

// TestBucket_PagedListing seeds MaxListLimit+1 keys under walk/ plus
// walk-other/x, which sorts before them, against fake-gcs-server. StartAfter
// travels as the inclusive StartOffset; the equal key must be skipped.
func TestBucket_PagedListing(t *testing.T) {
	t.Parallel()
	b := startBucket(t, gcs.MinChunkSize)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	want := make([]string, storage.MaxListLimit+1)
	for i := range want {
		want[i] = fmt.Sprintf("walk/%04d", i)
	}
	for _, key := range append(slices.Clone(want), "walk-other/x") {
		if _, err := b.Put(ctx, key, strings.NewReader("x"), storage.PutOptions{}); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
	}
	var got []string
	err := storage.Walk(ctx, b, storage.ListOptions{Prefix: "walk/"}, func(obj storage.Object) error {
		got = append(got, obj.Key)
		return nil
	})
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
		{name: "after prefix range", opts: storage.ListOptions{Prefix: "walk-other/", StartAfter: "walk/0000"}},
	} {
		if keys := listGCSKeys(ctx, t, b, tc.opts); !slices.Equal(keys, tc.want) {
			t.Errorf("%s: List = %v; want %v", tc.name, keys, tc.want)
		}
	}
	if _, err = b.List(ctx, storage.ListOptions{StartAfter: "walk/../x"}); !errors.Is(err, storage.ErrUnsafeKey) {
		t.Errorf("List invalid StartAfter = %v; want ErrUnsafeKey", err)
	}
}
