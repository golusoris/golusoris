// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package storagetest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"testing"

	"github.com/golusoris/golusoris/storage"
	"github.com/golusoris/golusoris/storage/storagetest"
)

type recordingTB struct {
	testing.TB
	failed bool
	logs   []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Failed() bool {
	return r.failed
}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.failed = true
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.failed = true
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
	runtime.Goexit()
}

func runInRecordingTB(fn func(tb testing.TB)) *recordingTB {
	rec := &recordingTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(rec)
	}()
	<-done
	return rec
}

type hookBucket struct {
	storage.Bucket
	listHook func(ctx context.Context, opts storage.ListOptions) ([]storage.Object, error)
	putHook  func(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.Object, error)
}

func (h *hookBucket) List(ctx context.Context, opts storage.ListOptions) ([]storage.Object, error) {
	if h.listHook != nil {
		return h.listHook(ctx, opts)
	}
	return h.Bucket.List(ctx, opts)
}

func (h *hookBucket) Put(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.Object, error) {
	if h.putHook != nil {
		return h.putHook(ctx, key, r, opts)
	}
	return h.Bucket.Put(ctx, key, r, opts)
}

func newLocalBucket(t *testing.T) *storage.LocalBucket {
	t.Helper()
	b, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	return b
}

func TestCheckPagedListing_Positive(t *testing.T) {
	t.Parallel()
	b := newLocalBucket(t)
	storagetest.CheckPagedListing(t.Context(), t, b)
}

func TestCheckPagedListing_Negative_OutOfOrder(t *testing.T) {
	t.Parallel()
	b := newLocalBucket(t)
	hook := &hookBucket{
		Bucket: b,
		listHook: func(ctx context.Context, opts storage.ListOptions) ([]storage.Object, error) {
			objs, err := b.List(ctx, opts)
			if err != nil {
				return nil, err
			}
			if len(objs) > 1 && opts.Prefix == "walk/" {
				slices.Reverse(objs)
			}
			return objs, nil
		},
	}
	rec := runInRecordingTB(func(tb testing.TB) {
		tb.Helper()
		storagetest.CheckPagedListing(t.Context(), tb, hook)
	})
	if !rec.failed {
		t.Fatal("CheckPagedListing succeeded on out-of-order bucket; want failure")
	}
}

// Only Walk sees the gap: every boundary case reads two keys from 0000-0001 or
// 0500-0501, or expects none.
func TestCheckPagedListing_Negative_WalkMissesKey(t *testing.T) {
	t.Parallel()
	b := newLocalBucket(t)
	hook := &hookBucket{
		Bucket: b,
		listHook: func(ctx context.Context, opts storage.ListOptions) ([]storage.Object, error) {
			objs, err := b.List(ctx, opts)
			return slices.DeleteFunc(objs, func(o storage.Object) bool { return o.Key == "walk/0300" }), err
		},
	}
	rec := runInRecordingTB(func(tb testing.TB) {
		tb.Helper()
		storagetest.CheckPagedListing(t.Context(), tb, hook)
	})
	if !rec.failed {
		t.Fatal("CheckPagedListing succeeded when Walk missed a key; want failure")
	}
}

func TestCheckPagedListing_Negative_IgnoresStartAfter(t *testing.T) {
	t.Parallel()
	b := newLocalBucket(t)
	hook := &hookBucket{
		Bucket: b,
		listHook: func(ctx context.Context, opts storage.ListOptions) ([]storage.Object, error) {
			if opts.Limit == 2 && opts.StartAfter != "" {
				opts.StartAfter = ""
			}
			return b.List(ctx, opts)
		},
	}
	rec := runInRecordingTB(func(tb testing.TB) {
		tb.Helper()
		storagetest.CheckPagedListing(t.Context(), tb, hook)
	})
	if !rec.failed {
		t.Fatal("CheckPagedListing succeeded when StartAfter ignored; want failure")
	}
}

func TestCheckPagedListing_Negative_UnsafeKeyAccepted(t *testing.T) {
	t.Parallel()
	b := newLocalBucket(t)
	hook := &hookBucket{
		Bucket: b,
		listHook: func(ctx context.Context, opts storage.ListOptions) ([]storage.Object, error) {
			if opts.StartAfter == "walk/../x" {
				return nil, nil
			}
			return b.List(ctx, opts)
		},
	}
	rec := runInRecordingTB(func(tb testing.TB) {
		tb.Helper()
		storagetest.CheckPagedListing(t.Context(), tb, hook)
	})
	if !rec.failed {
		t.Fatal("CheckPagedListing succeeded when unsafe key accepted; want failure")
	}
}

func TestCheckPagedListing_Negative_PutError(t *testing.T) {
	t.Parallel()
	b := newLocalBucket(t)
	hook := &hookBucket{
		Bucket: b,
		putHook: func(ctx context.Context, key string, r io.Reader, opts storage.PutOptions) (storage.Object, error) {
			return storage.Object{}, errors.New("simulated put failure")
		},
	}
	rec := runInRecordingTB(func(tb testing.TB) {
		tb.Helper()
		storagetest.CheckPagedListing(t.Context(), tb, hook)
	})
	if !rec.failed {
		t.Fatal("CheckPagedListing succeeded when Put failed; want failure")
	}
}

func TestCheckPagedListing_Boundary_CanceledContext(t *testing.T) {
	t.Parallel()
	b := newLocalBucket(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rec := runInRecordingTB(func(tb testing.TB) {
		tb.Helper()
		storagetest.CheckPagedListing(ctx, tb, b)
	})
	if !rec.failed {
		t.Fatal("CheckPagedListing succeeded with canceled context; want failure")
	}
}
