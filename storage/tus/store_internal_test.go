// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tusd "github.com/tus/tusd/v2/pkg/handler"

	"github.com/golusoris/golusoris/storage"
)

// newTestStore wires a bucketStore over a real LocalBucket + local scratch in
// temp dirs, returning scratch for durable completion assertions.
func newTestStore(t *testing.T) (*bucketStore, storage.Bucket, *localScratch) {
	t.Helper()
	bucket, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	log := slog.New(slog.DiscardHandler)
	store := newBucketStore(
		scratch, bucket, defaultKeyFunc("uploads/"), log,
		func(context.Context, string) error { return nil },
	)
	return store, bucket, scratch
}

func TestBucketStore_NewUploadWriteFinish(t *testing.T) {
	t.Parallel()
	store, bucket, scratch := newTestStore(t)
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

	// Completion is durable with the right key/size/metadata until delivery.
	record, err := scratch.Completion(ctx, info.ID)
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	ev := record.Upload.completed()
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
	store := newBucketStore(scratch, bucket, evil, slog.New(slog.DiscardHandler), nil)

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

func (failBucket) Get(context.Context, string) (io.ReadCloser, storage.Object, error) {
	return nil, storage.Object{}, storage.ErrNotFound
}

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
		scratch, failBucket{}, defaultKeyFunc("uploads/"), slog.New(slog.DiscardHandler), nil,
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

type observedReadCloser struct {
	r      io.Reader
	reads  int
	closed bool
}

func (r *observedReadCloser) Read(p []byte) (int, error) {
	r.reads++
	return r.r.Read(p)
}

func (r *observedReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestHashAndClose_BoundedAndCancelable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		body     string
		expected int64
		cancel   bool
		wantErr  bool
	}{
		{name: "exact", body: "data", expected: 4},
		{name: "short", body: "dat", expected: 4, wantErr: true},
		{name: "long", body: "data!", expected: 4, wantErr: true},
		{name: "canceled", body: "data", expected: 4, cancel: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			if tt.cancel {
				cancel()
			} else {
				defer cancel()
			}
			r := &observedReadCloser{r: strings.NewReader(tt.body)}
			sum, size, err := hashAndClose(ctx, r, tt.expected)
			if !tt.wantErr && err != nil {
				t.Fatalf("hashAndClose: %v", err)
			}
			if tt.wantErr && err == nil {
				t.Fatal("hashAndClose error = nil")
			}
			if !r.closed {
				t.Fatal("reader was not closed")
			}
			if tt.cancel && r.reads != 0 {
				t.Fatalf("canceled hash performed %d reads", r.reads)
			}
			if tt.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled hash error = %v", err)
			}
			if !tt.wantErr {
				if size != tt.expected || sum != sha256.Sum256([]byte(tt.body)) {
					t.Fatalf("hash result size=%d sum=%x", size, sum)
				}
			}
		})
	}
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

func TestLocalScratch_CreateDoesNotTruncateExistingUpload(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entry, err := scratch.Create(ctx, tusd.FileInfo{ID: "same", Size: 8})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err = entry.WriteChunk(ctx, 0, strings.NewReader("original")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	if _, err = scratch.Create(ctx, tusd.FileInfo{ID: "same", Size: 3}); err == nil {
		t.Fatal("duplicate Create succeeded")
	}
	rc, err := entry.GetReader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("duplicate Create changed bytes to %q", data)
	}
}

func TestLocalScratch_CreateCleansBinWhenInfoCreationFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	scratch, err := newLocalScratch(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "orphan.info"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = scratch.Create(context.Background(), tusd.FileInfo{ID: "orphan"}); err == nil {
		t.Fatal("Create succeeded with unusable info path")
	}
	if _, statErr := os.Stat(filepath.Join(root, "orphan")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("orphan bin state = %v; want absent", statErr)
	}
}

func TestLocalEntry_WriteChunkRejectsWrongOffset(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entry, err := scratch.Create(ctx, tusd.FileInfo{ID: "offset"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.WriteChunk(ctx, 0, strings.NewReader("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err = entry.WriteChunk(ctx, 0, strings.NewReader("duplicate")); err == nil {
		t.Fatal("WriteChunk accepted stale offset")
	}
	rc, err := entry.GetReader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "abc" {
		t.Fatalf("wrong-offset write changed bytes to %q", data)
	}
}

type cancelAfterRead struct {
	cancel context.CancelFunc
	done   bool
}

func (r *cancelAfterRead) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	p[0] = 'x'
	r.cancel()
	return 1, nil
}

func TestLocalEntry_WriteChunkHonorsCancellationDuringRead(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entry, err := scratch.Create(context.Background(), tusd.FileInfo{ID: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	n, err := entry.WriteChunk(ctx, 0, &cancelAfterRead{cancel: cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteChunk error = %v; want context.Canceled", err)
	}
	if n != 1 {
		t.Fatalf("WriteChunk wrote %d bytes; want committed partial byte", n)
	}
	info, infoErr := entry.GetInfo(context.Background())
	if infoErr != nil || info.Offset != 1 {
		t.Fatalf("offset = %d, err = %v; want 1", info.Offset, infoErr)
	}
}

func TestLocalScratch_PreCanceledOperationsPreserveRecoveryState(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	background := context.Background()
	entry, err := scratch.Create(background, tusd.FileInfo{ID: "preserve"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.WriteChunk(background, 0, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	original := newCompletionRecord(CompletedUpload{ID: "preserve", Key: "objects/original", Size: 4})
	if err = scratch.SaveCompletion(background, original); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(background)
	cancel()

	if _, getErr := scratch.Get(ctx, "preserve"); !errors.Is(getErr, context.Canceled) {
		t.Fatalf("Get error = %v; want context.Canceled", getErr)
	}
	updated := newCompletionRecord(CompletedUpload{ID: "preserve", Key: "objects/updated", Size: 4})
	if saveErr := scratch.SaveCompletion(ctx, updated); !errors.Is(saveErr, context.Canceled) {
		t.Fatalf("SaveCompletion error = %v; want context.Canceled", saveErr)
	}
	if _, completionErr := scratch.Completion(ctx, "preserve"); !errors.Is(completionErr, context.Canceled) {
		t.Fatalf("Completion error = %v; want context.Canceled", completionErr)
	}
	if reader, readerErr := entry.GetReader(ctx); !errors.Is(readerErr, context.Canceled) {
		if reader != nil {
			_ = reader.Close()
		}
		t.Fatalf("GetReader error = %v; want context.Canceled", readerErr)
	}
	if deleteErr := scratch.DeleteCompletion(ctx, "preserve"); !errors.Is(deleteErr, context.Canceled) {
		t.Fatalf("DeleteCompletion error = %v; want context.Canceled", deleteErr)
	}
	if removeErr := scratch.RemoveUpload(ctx, "preserve"); !errors.Is(removeErr, context.Canceled) {
		t.Fatalf("RemoveUpload error = %v; want context.Canceled", removeErr)
	}

	preserved, err := scratch.Completion(background, "preserve")
	if err != nil {
		t.Fatalf("preserved Completion: %v", err)
	}
	if preserved.Upload.Key != original.Upload.Key {
		t.Fatalf("completion key = %q; want %q", preserved.Upload.Key, original.Upload.Key)
	}
	preservedEntry, err := scratch.Get(background, "preserve")
	if err != nil {
		t.Fatalf("preserved Get: %v", err)
	}
	info, err := preservedEntry.GetInfo(background)
	if err != nil || info.Offset != 4 {
		t.Fatalf("preserved offset = %d, err = %v; want 4", info.Offset, err)
	}
}

func TestLocalScratch_DeleteCompletionSyncFailureIsIndeterminate(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := newCompletionRecord(CompletedUpload{ID: "ack", Key: "objects/ack", Size: 4})
	if err = scratch.SaveCompletion(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("injected receipt directory sync failure")
	scratch.syncParent = func(path string) error {
		if path != filepath.Join(scratch.root, "ack"+completionSuffix) {
			t.Fatalf("sync path = %q", path)
		}
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("receipt state during sync = %v; want absent", statErr)
		}
		return wantErr
	}
	if err = scratch.DeleteCompletion(context.Background(), "ack"); !errors.Is(err, wantErr) {
		t.Fatalf("DeleteCompletion error = %v; want injected sync error", err)
	}
	if _, err = scratch.Completion(context.Background(), "ack"); !errors.Is(err, tusd.ErrNotFound) {
		t.Fatalf("Completion after indeterminate delete = %v; want not found", err)
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
