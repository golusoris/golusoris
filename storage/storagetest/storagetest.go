// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package storagetest provides shared conformance helpers for testing
// [storage.Bucket] implementations.
package storagetest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/storage"
)

// CheckPagedListing verifies that b satisfies paged listing, Walk ordering,
// StartAfter boundary semantics, and invalid StartAfter rejection.
func CheckPagedListing(ctx context.Context, tb testing.TB, b storage.Bucket) {
	tb.Helper()
	want := seedPagedKeys(ctx, tb, b, storage.MaxListLimit+1)
	if tb.Failed() {
		return
	}
	verifyPagedWalk(ctx, tb, b, want)
	if tb.Failed() {
		return
	}
	verifyPagedBoundaries(ctx, tb, b, want)
	verifyInvalidStartAfter(ctx, tb, b)
}

func seedPagedKeys(ctx context.Context, tb testing.TB, b storage.Bucket, n int) []string {
	tb.Helper()
	want := make([]string, n)
	for i := range want {
		want[i] = fmt.Sprintf("walk/%04d", i)
	}
	for _, key := range append(slices.Clone(want), "walk-other/x") {
		if _, err := b.Put(ctx, key, strings.NewReader("x"), storage.PutOptions{}); err != nil {
			tb.Fatalf("Put %q: %v", key, err)
			return nil
		}
	}
	return want
}

func verifyPagedWalk(ctx context.Context, tb testing.TB, b storage.Bucket, want []string) {
	tb.Helper()
	var got []string
	err := storage.Walk(ctx, b, storage.ListOptions{Prefix: "walk/"}, func(obj storage.Object) error {
		got = append(got, obj.Key)
		return nil
	})
	if err != nil || !slices.Equal(got, want) {
		tb.Fatalf("Walk = %d keys, %v; want %d in order", len(got), err, len(want))
	}
}

type listBoundaryCase struct {
	name string
	opts storage.ListOptions
	want []string
}

func boundaryCases(want []string) []listBoundaryCase {
	return []listBoundaryCase{
		{name: "last key", opts: storage.ListOptions{Prefix: "walk/", StartAfter: want[len(want)-1]}},
		{name: "existing key", opts: storage.ListOptions{Prefix: "walk/", StartAfter: "walk/0499", Limit: 2}, want: want[500:502]},
		{name: "between keys", opts: storage.ListOptions{Prefix: "walk/", StartAfter: "walk/0499x", Limit: 2}, want: want[500:502]},
		{name: "before prefix range", opts: storage.ListOptions{Prefix: "walk/", StartAfter: "walk-other/x", Limit: 2}, want: want[:2]},
		{name: "no prefix", opts: storage.ListOptions{StartAfter: "walk-other/x", Limit: 2}, want: want[:2]},
		{name: "after prefix range", opts: storage.ListOptions{Prefix: "walk-other/", StartAfter: "walk/0000"}},
	}
}

func verifyPagedBoundaries(ctx context.Context, tb testing.TB, b storage.Bucket, want []string) {
	tb.Helper()
	for _, tc := range boundaryCases(want) {
		keys := listPagedKeys(ctx, tb, b, tc.opts)
		if !slices.Equal(keys, tc.want) {
			tb.Errorf("%s: List = %v; want %v", tc.name, keys, tc.want)
		}
	}
}

func listPagedKeys(ctx context.Context, tb testing.TB, b storage.Bucket, opts storage.ListOptions) []string {
	tb.Helper()
	objects, err := b.List(ctx, opts)
	if err != nil {
		tb.Fatalf("List(%+v): %v", opts, err)
		return nil
	}
	keys := make([]string, 0, len(objects))
	for _, obj := range objects {
		keys = append(keys, obj.Key)
	}
	return keys
}

func verifyInvalidStartAfter(ctx context.Context, tb testing.TB, b storage.Bucket) {
	tb.Helper()
	if _, err := b.List(ctx, storage.ListOptions{StartAfter: "walk/../x"}); !errors.Is(err, storage.ErrUnsafeKey) {
		tb.Errorf("List invalid StartAfter = %v; want ErrUnsafeKey", err)
	}
}
