// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storage_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/storage"
)

func TestNormalizeListLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want int
		wantErr  bool
	}{
		{in: 0, want: storage.DefaultListLimit},
		{in: 1, want: 1},
		{in: storage.MaxListLimit, want: storage.MaxListLimit},
		{in: -1, wantErr: true},
		{in: storage.MaxListLimit + 1, wantErr: true},
	} {
		got, err := storage.NormalizeListLimit(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("NormalizeListLimit(%d) = %d, %v; want %d, err %v", tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestCleanListPrefix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in, want string
		wantErr  bool
	}{
		{in: "", want: ""},
		{in: "videos/", want: "videos/"},
		{in: "videos/2026", want: "videos/2026"},
		{in: "../escape", wantErr: true},
		{in: "a/./b", wantErr: true},
		{in: "a//", wantErr: true},
		{in: "/abs", wantErr: true},
	} {
		got, err := storage.CleanListPrefix(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("CleanListPrefix(%q) = %q, %v; want %q, err %v", tc.in, got, err, tc.want, tc.wantErr)
		}
		if tc.wantErr && !errors.Is(err, storage.ErrUnsafeKey) {
			t.Errorf("CleanListPrefix(%q) error = %v, want ErrUnsafeKey", tc.in, err)
		}
	}
}

func TestNormalizeListOptions(t *testing.T) {
	t.Parallel()
	longKey := strings.Repeat("k", storage.MaxKeyBytes)
	for _, tc := range []struct {
		name              string
		in                storage.ListOptions
		wantAfter         string
		wantEmpty, wantOK bool
	}{
		{name: "zero", in: storage.ListOptions{}, wantOK: true},
		{name: "inside prefix", in: storage.ListOptions{Prefix: "b/", StartAfter: "b/m"}, wantAfter: "b/m", wantOK: true},
		{name: "equals prefix", in: storage.ListOptions{Prefix: "b/", StartAfter: "b/"}, wantAfter: "b/", wantOK: true},
		{name: "no prefix", in: storage.ListOptions{StartAfter: "z/y"}, wantAfter: "z/y", wantOK: true},
		{name: "before prefix range", in: storage.ListOptions{Prefix: "b/", StartAfter: "a/z"}, wantOK: true},
		{name: "byte before slash", in: storage.ListOptions{Prefix: "b/", StartAfter: "b-z"}, wantOK: true},
		{name: "after prefix range", in: storage.ListOptions{Prefix: "b/", StartAfter: "c"}, wantEmpty: true, wantOK: true},
		{name: "max key bytes", in: storage.ListOptions{StartAfter: longKey}, wantAfter: longKey, wantOK: true},
		{name: "over max key bytes", in: storage.ListOptions{StartAfter: longKey + "k"}},
		{name: "traversal", in: storage.ListOptions{StartAfter: "../x"}},
		{name: "empty segment", in: storage.ListOptions{StartAfter: "a//b"}},
		{name: "absolute", in: storage.ListOptions{StartAfter: "/a"}},
		{name: "null byte", in: storage.ListOptions{StartAfter: "a\x00"}},
	} {
		got, empty, err := storage.NormalizeListOptions(tc.in)
		if tc.wantOK != (err == nil) || (!tc.wantOK && !errors.Is(err, storage.ErrUnsafeKey)) {
			t.Errorf("%s: error = %v; want ok %v or ErrUnsafeKey", tc.name, err, tc.wantOK)
			continue
		}
		if got.StartAfter != tc.wantAfter || empty != tc.wantEmpty {
			t.Errorf("%s: StartAfter %q, empty %v; want %q, %v", tc.name, got.StartAfter, empty, tc.wantAfter, tc.wantEmpty)
		}
		if tc.wantOK && got.Limit != storage.DefaultListLimit {
			t.Errorf("%s: Limit = %d; want default", tc.name, got.Limit)
		}
	}
	if _, _, err := storage.NormalizeListOptions(storage.ListOptions{Limit: storage.MaxListLimit + 1}); err == nil {
		t.Error("over-limit options accepted")
	}
}

// sliceBucket serves List from sorted keys. ignoreStartAfter models a backend
// that drops the start position; only List is implemented.
type sliceBucket struct {
	storage.Bucket

	keys             []string
	ignoreStartAfter bool
	calls            int
}

func (b *sliceBucket) List(_ context.Context, opts storage.ListOptions) ([]storage.Object, error) {
	b.calls++
	query, empty, err := storage.NormalizeListOptions(opts)
	if err != nil || empty {
		return nil, err
	}
	out := make([]storage.Object, 0, query.Limit)
	for _, key := range b.keys {
		if len(out) == query.Limit {
			break
		}
		if strings.HasPrefix(key, query.Prefix) && (b.ignoreStartAfter || key > query.StartAfter) {
			out = append(out, storage.Object{Key: key})
		}
	}
	return out, nil
}

func numberedKeys(prefix string, n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("%s%04d", prefix, i)
	}
	return keys
}

func walkKeys(ctx context.Context, b storage.Bucket, opts storage.ListOptions) ([]string, error) {
	var keys []string
	err := storage.Walk(ctx, b, opts, func(obj storage.Object) error {
		keys = append(keys, obj.Key)
		return nil
	})
	return keys, err
}

func TestWalk_ListsEveryPageInOrder(t *testing.T) {
	t.Parallel()
	want := numberedKeys("k/", 2*storage.MaxListLimit+500)
	b := &sliceBucket{keys: append(slices.Clone(want), "z/other")}
	got, err := walkKeys(context.Background(), b, storage.ListOptions{Prefix: "k/"})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Walk returned %d keys; want %d in order", len(got), len(want))
	}
	if b.calls != 4 {
		t.Fatalf("Walk made %d List calls; want 3 pages plus the empty one", b.calls)
	}
}

func TestWalk_StartAfter(t *testing.T) {
	t.Parallel()
	keys := numberedKeys("k/", 20)
	for _, tc := range []struct {
		name, after string
		want        []string
	}{
		{name: "between keys", after: "k/0016x", want: keys[17:]},
		{name: "existing key", after: "k/0016", want: keys[17:]},
		{name: "last key", after: "k/0019"},
		{name: "before range", after: "a", want: keys},
	} {
		b := &sliceBucket{keys: keys}
		got, err := walkKeys(context.Background(), b, storage.ListOptions{Limit: 2, StartAfter: tc.after})
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: Walk = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
}

func TestWalk_RejectsBackendIgnoringStartAfter(t *testing.T) {
	t.Parallel()
	b := &sliceBucket{keys: numberedKeys("k/", 5), ignoreStartAfter: true}
	got, err := walkKeys(context.Background(), b, storage.ListOptions{Limit: 2})
	if !errors.Is(err, storage.ErrListOrder) {
		t.Fatalf("Walk error = %v; want ErrListOrder", err)
	}
	if len(got) != 2 || b.calls != 2 {
		t.Fatalf("Walk visited %v over %d calls; want first page only", got, b.calls)
	}
}

func TestWalk_StopsOnCallbackErrorAndCancellation(t *testing.T) {
	t.Parallel()
	stop := errors.New("stop")
	b := &sliceBucket{keys: numberedKeys("k/", 10)}
	visited := 0
	err := storage.Walk(context.Background(), b, storage.ListOptions{Limit: 2}, func(storage.Object) error {
		visited++
		if visited == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || visited != 3 || b.calls != 2 {
		t.Fatalf("Walk = %v after %d objects, %d calls; want stop at third object", err, visited, b.calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	idle := &sliceBucket{keys: numberedKeys("k/", 10)}
	if _, err = walkKeys(ctx, idle, storage.ListOptions{}); !errors.Is(err, context.Canceled) || idle.calls != 0 {
		t.Fatalf("canceled Walk = %v after %d calls; want context.Canceled before List", err, idle.calls)
	}
	_, err = walkKeys(context.Background(), idle, storage.ListOptions{StartAfter: "../x"})
	if !errors.Is(err, storage.ErrUnsafeKey) {
		t.Fatalf("Walk invalid StartAfter = %v; want ErrUnsafeKey", err)
	}
}
