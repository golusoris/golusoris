// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tusd "github.com/tus/tusd/v2/pkg/handler"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/storage"
)

// newTestStore wires a bucketStore over a real LocalBucket + local scratch in
// temp dirs, capturing completion events for assertions.
func newTestStore(t *testing.T) (*bucketStore, storage.Bucket, *[]CompletedUpload) {
	t.Helper()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	var got []CompletedUpload
	onFinish := func(_ context.Context, c CompletedUpload) error {
		got = append(got, c)
		return nil
	}
	log := slog.New(slog.DiscardHandler)
	store := newBucketStore(
		scratch, bucket, defaultKeyFunc("uploads/"), clock.NewFake(), log, onFinish,
	)
	return store, bucket, &got
}

func TestBucketStore_NewUploadWriteFinish(t *testing.T) {
	t.Parallel()
	store, bucket, completed := newTestStore(t)
	ctx := context.Background()

	up, err := store.NewUpload(ctx, tusd.FileInfo{Size: 11, MetaData: tusd.MetaData{"filename": "a.txt"}})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	info, err := up.GetInfo(ctx)
	if err != nil || info.Offset != 0 {
		t.Fatalf("fresh upload: offset=%d err=%v", info.Offset, err)
	}

	// Two sequential chunks accumulate at the running offset.
	if _, err = up.WriteChunk(ctx, 0, strings.NewReader("hello ")); err != nil {
		t.Fatalf("WriteChunk 1: %v", err)
	}
	if _, err = up.WriteChunk(ctx, 6, strings.NewReader("world")); err != nil {
		t.Fatalf("WriteChunk 2: %v", err)
	}
	if info, err = up.GetInfo(ctx); err != nil || info.Offset != 11 {
		t.Fatalf("after writes: offset=%d err=%v", info.Offset, err)
	}

	if err = up.FinishUpload(ctx); err != nil {
		t.Fatalf("FinishUpload: %v", err)
	}

	// Bytes + size land in the bucket under uploads/<id>.
	key := "uploads/" + info.ID
	rc, obj, err := bucket.Get(ctx, key)
	if err != nil {
		t.Fatalf("bucket.Get %q: %v", key, err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if string(body) != "hello world" {
		t.Fatalf("bucket body = %q", body)
	}
	if obj.Size != 11 {
		t.Fatalf("bucket size = %d", obj.Size)
	}

	// OnComplete fired exactly once with the right key/size/metadata.
	if len(*completed) != 1 {
		t.Fatalf("completed count = %d", len(*completed))
	}
	ev := (*completed)[0]
	if ev.Key != key || ev.Size != 11 || ev.MetaData["filename"] != "a.txt" {
		t.Fatalf("completion mismatch: %+v", ev)
	}
}

func TestBucketStore_ResumeAfterInterrupt(t *testing.T) {
	t.Parallel()
	store, _, _ := newTestStore(t)
	ctx := context.Background()

	up, err := store.NewUpload(ctx, tusd.FileInfo{Size: 6})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if _, err = up.WriteChunk(ctx, 0, strings.NewReader("abc")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	info, _ := up.GetInfo(ctx)

	// Simulate a resumed HEAD: GetUpload re-reads scratch and reports offset 3.
	resumed, err := store.GetUpload(ctx, info.ID)
	if err != nil {
		t.Fatalf("GetUpload: %v", err)
	}
	rInfo, _ := resumed.GetInfo(ctx)
	if rInfo.Offset != 3 {
		t.Fatalf("resumed offset = %d, want 3", rInfo.Offset)
	}
	if _, err = resumed.WriteChunk(ctx, 3, strings.NewReader("def")); err != nil {
		t.Fatalf("resumed WriteChunk: %v", err)
	}
	final, _ := resumed.GetInfo(ctx)
	if final.Offset != 6 {
		t.Fatalf("final offset = %d, want 6", final.Offset)
	}
}

func TestBucketStore_Terminate(t *testing.T) {
	t.Parallel()
	store, _, _ := newTestStore(t)
	ctx := context.Background()

	up, err := store.NewUpload(ctx, tusd.FileInfo{Size: 3})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	info, _ := up.GetInfo(ctx)

	term := store.AsTerminatableUpload(up)
	if err = term.Terminate(ctx); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	// Scratch state is gone: GetUpload now reports not-found.
	if _, err = store.GetUpload(ctx, info.ID); err == nil {
		t.Fatal("expected ErrNotFound after Terminate")
	}
}

func TestBucketStore_DeclareLength(t *testing.T) {
	t.Parallel()
	store, _, _ := newTestStore(t)
	ctx := context.Background()

	up, err := store.NewUpload(ctx, tusd.FileInfo{SizeIsDeferred: true})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	decl := store.AsLengthDeclarableUpload(up)
	if err = decl.DeclareLength(ctx, 42); err != nil {
		t.Fatalf("DeclareLength: %v", err)
	}
	info, _ := up.GetInfo(ctx)
	if info.Size != 42 || info.SizeIsDeferred {
		t.Fatalf("after declare: size=%d deferred=%v", info.Size, info.SizeIsDeferred)
	}
}

func TestBucketStore_GetReader(t *testing.T) {
	t.Parallel()
	store, _, _ := newTestStore(t)
	ctx := context.Background()

	up, err := store.NewUpload(ctx, tusd.FileInfo{Size: 5})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if _, err = up.WriteChunk(ctx, 0, strings.NewReader("bytes")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	rc, err := up.GetReader(ctx)
	if err != nil {
		t.Fatalf("GetReader: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if string(body) != "bytes" {
		t.Fatalf("reader body = %q", body)
	}
}

func TestDefaultKeyFunc(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		prefix  string
		id      string
		want    string
		wantErr bool
	}{
		{"simple", "uploads/", "abc123", "uploads/abc123", false},
		{"no prefix", "", "abc123", "abc123", false},
		{"trim slashes", "/uploads/", "id", "uploads/id", false},
		{"reject traversal", "uploads/", "../etc/passwd", "", true},
		{"reject slash", "uploads/", "a/b", "", true},
		{"reject nul", "uploads/", "a\x00b", "", true},
		{"reject empty", "uploads/", "", "", true},
		{"reject dotdot", "uploads/", "..", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := defaultKeyFunc(tt.prefix)(tusd.FileInfo{ID: tt.id})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("key = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		key     string
		want    string
		wantErr bool
	}{
		{"plain", "uploads/x.txt", "uploads/x.txt", false},
		{"leading slash", "/uploads/x", "uploads/x", false},
		{"traversal", "uploads/../etc", "", true},
		{"dotdot prefix", "../x", "", true},
		{"nul", "a\x00b", "", true},
		{"backslash", "a\\b", "", true},
		{"empty", "", "", true},
		{"root only", "/", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := sanitizeKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("key = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFinishUploadRejectsBadKey(t *testing.T) {
	t.Parallel()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	// KeyFunc deliberately returns a traversal key; FinishUpload must reject it.
	evil := func(tusd.FileInfo) (string, error) { return "../escape", nil }
	store := newBucketStore(scratch, bucket, evil, clock.NewFake(), slog.New(slog.DiscardHandler), nil)

	ctx := context.Background()
	up, err := store.NewUpload(ctx, tusd.FileInfo{Size: 1})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if _, err = up.WriteChunk(ctx, 0, bytes.NewReader([]byte("x"))); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	if err = up.FinishUpload(ctx); err == nil {
		t.Fatal("expected FinishUpload to reject traversal key")
	}
}

// failBucket is a storage.Bucket whose Put always errors, to drive the
// FinishUpload persist-failure branch.
type failBucket struct{ storage.Bucket }

func (failBucket) Put(
	context.Context, string, io.Reader, storage.PutOptions,
) (storage.Object, error) {
	return storage.Object{}, errBoom
}

func TestFinishUpload_BucketPutError(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	store := newBucketStore(
		scratch, failBucket{}, defaultKeyFunc("uploads/"),
		clock.NewFake(), slog.New(slog.DiscardHandler), nil,
	)
	ctx := context.Background()
	up, err := store.NewUpload(ctx, tusd.FileInfo{Size: 1})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if _, err = up.WriteChunk(ctx, 0, strings.NewReader("x")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	if err = up.FinishUpload(ctx); err == nil {
		t.Fatal("expected FinishUpload to surface bucket Put error")
	}
}

func TestBucketUpload_ResolveKey(t *testing.T) {
	t.Parallel()

	t.Run("valid key is sanitized", func(t *testing.T) {
		t.Parallel()
		u := &bucketUpload{
			store: &bucketStore{keyFn: func(tusd.FileInfo) (string, error) { return "/uploads/x", nil }},
			id:    "x",
		}
		got, err := u.resolveKey(tusd.FileInfo{})
		if err != nil {
			t.Fatalf("resolveKey: %v", err)
		}
		if got != "uploads/x" {
			t.Fatalf("key = %q, want %q", got, "uploads/x")
		}
	})

	t.Run("keyFn error propagates", func(t *testing.T) {
		t.Parallel()
		u := &bucketUpload{
			store: &bucketStore{keyFn: func(tusd.FileInfo) (string, error) { return "", errBoom }},
			id:    "x",
		}
		if _, err := u.resolveKey(tusd.FileInfo{}); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom", err)
		}
	})

	t.Run("traversal key rejected by sanitizeKey", func(t *testing.T) {
		t.Parallel()
		u := &bucketUpload{
			store: &bucketStore{keyFn: func(tusd.FileInfo) (string, error) { return "../escape", nil }},
			id:    "x",
		}
		if _, err := u.resolveKey(tusd.FileInfo{}); err == nil {
			t.Fatal("expected sanitizeKey to reject a traversal key")
		}
	})
}

// closeErrReader wraps an io.Reader, returning a fixed error from Close — it
// drives putAndClose's close-error handling independently of the Put result.
type closeErrReader struct {
	io.Reader
	closeErr error
}

func (c closeErrReader) Close() error { return c.closeErr }

func TestBucketUpload_PutAndClose(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		bucket, err := storage.NewLocalBucket(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalBucket: %v", err)
		}
		u := &bucketUpload{store: &bucketStore{bucket: bucket}, id: "x"}
		rc := closeErrReader{Reader: strings.NewReader("data")}
		obj, err := u.putAndClose(context.Background(), "k", rc, tusd.FileInfo{})
		if err != nil {
			t.Fatalf("putAndClose: %v", err)
		}
		if obj.Size != 4 {
			t.Fatalf("obj.Size = %d, want 4", obj.Size)
		}
	})

	t.Run("put error takes precedence over a close error", func(t *testing.T) {
		t.Parallel()
		u := &bucketUpload{store: &bucketStore{bucket: failBucket{}}, id: "x"}
		rc := closeErrReader{Reader: strings.NewReader("data"), closeErr: errors.New("close boom")}
		if _, err := u.putAndClose(context.Background(), "k", rc, tusd.FileInfo{}); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want errBoom (Put's error, not the close error)", err)
		}
	})

	t.Run("close error surfaces when put succeeds", func(t *testing.T) {
		t.Parallel()
		bucket, err := storage.NewLocalBucket(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocalBucket: %v", err)
		}
		u := &bucketUpload{store: &bucketStore{bucket: bucket}, id: "x"}
		wantErr := errors.New("close boom")
		rc := closeErrReader{Reader: strings.NewReader("data"), closeErr: wantErr}
		if _, err = u.putAndClose(context.Background(), "k", rc, tusd.FileInfo{}); !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want %v", err, wantErr)
		}
	})
}

// termEntry is a scratchEntry whose Terminate result is configurable; the
// other methods are unused by the cleanupScratch tests that construct it.
type termEntry struct{ err error }

func (termEntry) WriteChunk(context.Context, int64, io.Reader) (int64, error) { return 0, nil }
func (termEntry) GetInfo(context.Context) (tusd.FileInfo, error)              { return tusd.FileInfo{}, nil }

func (termEntry) GetReader(context.Context) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (termEntry) DeclareLength(context.Context, int64) error { return nil }
func (e termEntry) Terminate(context.Context) error          { return e.err }

func TestBucketUpload_CleanupScratch(t *testing.T) {
	t.Parallel()

	t.Run("success is silent", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		u := &bucketUpload{
			store: &bucketStore{log: slog.New(slog.NewTextHandler(&buf, nil))},
			entry: termEntry{},
			id:    "x",
		}
		u.cleanupScratch(context.Background())
		if buf.Len() != 0 {
			t.Errorf("unexpected log output on success: %s", buf.String())
		}
	})

	t.Run("terminate error is logged, not returned", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		u := &bucketUpload{
			store: &bucketStore{log: slog.New(slog.NewTextHandler(&buf, nil))},
			entry: termEntry{err: errBoom},
			id:    "x",
		}
		u.cleanupScratch(context.Background()) // no return value: must not panic
		if !strings.Contains(buf.String(), "scratch cleanup failed") {
			t.Errorf("expected a cleanup-failed log entry, got %q", buf.String())
		}
	})
}

func TestBucketUpload_NotifyFinish(t *testing.T) {
	t.Parallel()

	t.Run("nil hook is a no-op", func(t *testing.T) {
		t.Parallel()
		u := &bucketUpload{store: &bucketStore{onFinish: nil}, id: "x"}
		if err := u.notifyFinish(context.Background(), CompletedUpload{ID: "x"}); err != nil {
			t.Fatalf("notifyFinish: %v", err)
		}
	})

	t.Run("hook success receives the completed value", func(t *testing.T) {
		t.Parallel()
		var got CompletedUpload
		u := &bucketUpload{store: &bucketStore{onFinish: func(_ context.Context, c CompletedUpload) error {
			got = c
			return nil
		}}, id: "x"}
		want := CompletedUpload{ID: "x", Key: "k", Size: 5}
		if err := u.notifyFinish(context.Background(), want); err != nil {
			t.Fatalf("notifyFinish: %v", err)
		}
		if got.ID != want.ID || got.Key != want.Key || got.Size != want.Size {
			t.Fatalf("hook received %+v, want %+v", got, want)
		}
	})

	t.Run("hook error is wrapped", func(t *testing.T) {
		t.Parallel()
		u := &bucketUpload{store: &bucketStore{onFinish: func(context.Context, CompletedUpload) error {
			return errBoom
		}}, id: "x"}
		err := u.notifyFinish(context.Background(), CompletedUpload{})
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v, want wrapped errBoom", err)
		}
	})
}

func TestScratch_GetReaderAndWriteOnRemoved(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	ctx := context.Background()
	entry, err := scratch.Create(ctx, tusd.FileInfo{ID: "gone"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err = entry.Terminate(ctx); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	// Bin file removed: reader + append must error rather than panic.
	if _, err = entry.GetReader(ctx); err == nil {
		t.Fatal("GetReader on removed scratch should error")
	}
	if _, err = entry.WriteChunk(ctx, 0, strings.NewReader("y")); err == nil {
		t.Fatal("WriteChunk on removed scratch should error")
	}
}

func TestScratch_ExpiredMissingDir(t *testing.T) {
	t.Parallel()
	scratch := &localScratch{root: filepath.Join(t.TempDir(), "absent")}
	if _, err := scratch.Expired(context.Background(), time.Now(), time.Hour); err == nil {
		t.Fatal("Expired on missing dir should error")
	}
}

func TestPutOptionsContentType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		meta tusd.MetaData
		want string
	}{
		{"filetype", tusd.MetaData{"filetype": "image/png"}, "image/png"},
		{"type fallback", tusd.MetaData{"type": "text/plain"}, "text/plain"},
		{"none", tusd.MetaData{"filename": "a"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := putOptions(tusd.FileInfo{MetaData: tt.meta})
			if got.ContentType != tt.want {
				t.Fatalf("content type = %q, want %q", got.ContentType, tt.want)
			}
		})
	}
}

// failEntry is a scratchEntry whose every method errors, to drive the
// error-wrapping branches of the bucketUpload delegating methods.
type failEntry struct{}

func (failEntry) WriteChunk(context.Context, int64, io.Reader) (int64, error) {
	return 0, errBoom
}
func (failEntry) GetInfo(context.Context) (tusd.FileInfo, error) { return tusd.FileInfo{}, errBoom }
func (failEntry) GetReader(context.Context) (io.ReadCloser, error) {
	return nil, errBoom
}
func (failEntry) DeclareLength(context.Context, int64) error { return errBoom }
func (failEntry) Terminate(context.Context) error            { return errBoom }

func TestBucketUpload_WrapsEntryErrors(t *testing.T) {
	t.Parallel()
	up := &bucketUpload{entry: failEntry{}, id: "abc"}
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
	}{
		{"WriteChunk", func() error { _, err := up.WriteChunk(ctx, 0, strings.NewReader("x")); return err }},
		{"GetInfo", func() error { _, err := up.GetInfo(ctx); return err }},
		{"GetReader", func() error { _, err := up.GetReader(ctx); return err }},
		{"DeclareLength", func() error { return up.DeclareLength(ctx, 1) }},
		{"Terminate", func() error { return up.Terminate(ctx) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.call()
			if err == nil {
				t.Fatalf("%s: expected error", tt.name)
			}
			if !errors.Is(err, errBoom) {
				t.Fatalf("%s: error not wrapped: %v", tt.name, err)
			}
			if !strings.HasPrefix(err.Error(), "tus: ") {
				t.Fatalf("%s: missing tus prefix: %v", tt.name, err)
			}
		})
	}
}

func TestLocalScratch_RejectsBadID(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	for _, id := range []string{"", "../x", "a/b", "a\x00b", "..", "a..b"} {
		if _, err := scratch.Create(context.Background(), tusd.FileInfo{ID: id}); err == nil {
			t.Fatalf("Create accepted bad id %q", id)
		}
	}
}
