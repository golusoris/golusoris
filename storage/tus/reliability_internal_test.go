// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
	tusd "github.com/tus/tusd/v2/pkg/handler"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/storage"
)

type putCountingBucket struct {
	storage.Bucket
	puts atomic.Int32
}

type failCompletionSaveScratch struct {
	scratchStore
	failAt int32
	saves  atomic.Int32
}

func (s *failCompletionSaveScratch) SaveCompletion(
	ctx context.Context, record completionRecord,
) error {
	if s.saves.Add(1) == s.failAt {
		return errBoom
	}
	return s.scratchStore.SaveCompletion(ctx, record)
}

type errorAfterPutBucket struct {
	storage.Bucket
	puts atomic.Int32
}

type probeFailBucket struct {
	*putCountingBucket
}

func (probeFailBucket) Get(context.Context, string) (io.ReadCloser, storage.Object, error) {
	return nil, storage.Object{}, errBoom
}

func (b *errorAfterPutBucket) Put(
	ctx context.Context, key string, src io.Reader, opts storage.PutOptions,
) (storage.Object, error) {
	obj, err := b.Bucket.Put(ctx, key, src, opts)
	if err == nil && b.puts.Add(1) == 1 {
		return storage.Object{}, errBoom
	}
	return obj, err
}

func (b *putCountingBucket) Put(
	ctx context.Context, key string, src io.Reader, opts storage.PutOptions,
) (storage.Object, error) {
	b.puts.Add(1)
	return b.Bucket.Put(ctx, key, src, opts)
}

func createTestUpload(t *testing.T, client *http.Client, base string, size int) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base, nil)
	if err != nil {
		t.Fatalf("new POST: %v", err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", strconv.Itoa(size))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func finishTestUpload(t *testing.T, client *http.Client, url, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new PATCH: %v", err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Offset", "0")
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PATCH upload: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

type retryRig struct {
	h        *Handler
	clk      *clockwork.FakeClock
	bucket   *putCountingBucket
	srv      *httptest.Server
	attempts chan int32
}

// newRetryRig starts a handler whose callback fails its first failures attempts
// and reports every attempt number on attempts.
func newRetryRig(t *testing.T, failures int32) *retryRig {
	t.Helper()
	local, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	rig := &retryRig{
		clk:      clockwork.NewFakeClock(),
		bucket:   &putCountingBucket{Bucket: local},
		attempts: make(chan int32, failures+2),
	}
	opts := defaultOptions()
	opts.Enabled = true
	opts.ScratchDir = t.TempDir()
	opts.GracefulRequestCompletionTimeout = time.Millisecond
	lc := fxtest.NewLifecycle(t)
	rig.h, err = newHandler(params{
		LC: lc, Opts: opts, Bucket: rig.bucket,
		Logger: slog.New(slog.DiscardHandler), Clock: rig.clk,
	})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	var attempt atomic.Int32
	mustOnComplete(t, rig.h, "retry", func(context.Context, CompletedUpload) error {
		n := attempt.Add(1)
		rig.attempts <- n
		if n <= failures {
			return errBoom
		}
		return nil
	})
	if err = lc.Start(context.Background()); err != nil {
		t.Fatalf("lifecycle start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if stopErr := lc.Stop(stopCtx); stopErr != nil {
			t.Errorf("lifecycle stop: %v", stopErr)
		}
	})
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = rig.clk.BlockUntilContext(waitCtx, 1); err != nil {
		t.Fatalf("completion retry ticker did not start: %v", err)
	}
	router := chi.NewRouter()
	rig.h.Mount(router)
	rig.srv = httptest.NewServer(router)
	t.Cleanup(rig.srv.Close)
	return rig
}

// finishFailedUpload completes one upload whose inline callback fails, then
// waits until no request holds its lock so the next tick cannot skip it.
func (r *retryRig) finishFailedUpload(t *testing.T) {
	t.Helper()
	url := createTestUpload(t, r.srv.Client(), r.srv.URL+r.h.BasePath(), 4)
	if status := finishTestUpload(t, r.srv.Client(), url, "data"); status != http.StatusInternalServerError {
		t.Fatalf("PATCH status = %d, want 500 from transient callback failure", status)
	}
	r.expectAttempt(t, 1)
	id := path.Base(url)
	for range 1000 {
		if unlock, ok := r.h.locker.tryMaintenanceLock(id); ok {
			unlock()
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("upload %s still locked after its PATCH returned", id)
}

func (r *retryRig) expectAttempt(t *testing.T, want int32) {
	t.Helper()
	select {
	case got := <-r.attempts:
		if got != want {
			t.Fatalf("callback attempt = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("completion callback attempt %d did not run", want)
	}
}

func (r *retryRig) expectNoAttempt(t *testing.T) {
	t.Helper()
	select {
	case got := <-r.attempts:
		t.Fatalf("acknowledged completion redelivered as attempt %d", got)
	case <-time.After(25 * time.Millisecond):
	}
}

// TestCompletionRetryIsDurable verifies that a transient callback failure does
// not fail or lose the completed upload, repeat the Bucket Put, or redeliver
// after the durable completion has been acknowledged.
func TestCompletionRetryIsDurable(t *testing.T) {
	t.Parallel()
	rig := newRetryRig(t, 1)
	rig.finishFailedUpload(t)
	if rig.bucket.puts.Load() != 1 {
		t.Fatalf("Bucket Put count = %d, want 1", rig.bucket.puts.Load())
	}

	rig.clk.Advance(15 * time.Minute)
	rig.expectAttempt(t, 2)
	if rig.bucket.puts.Load() != 1 {
		t.Fatalf("Bucket Put count after retry = %d, want 1", rig.bucket.puts.Load())
	}

	rig.clk.Advance(15 * time.Minute)
	rig.expectNoAttempt(t)
}

// TestCompletionRetryRunsEveryTick verifies consecutive ticks each retry a
// failing completion; a scan parked at directory end once skipped every other
// tick (#689).
func TestCompletionRetryRunsEveryTick(t *testing.T) {
	t.Parallel()
	rig := newRetryRig(t, 2)
	rig.finishFailedUpload(t)
	for want := int32(2); want <= 3; want++ {
		rig.clk.Advance(15 * time.Minute)
		rig.expectAttempt(t, want)
	}
	rig.clk.Advance(15 * time.Minute)
	rig.expectNoAttempt(t)
	if rig.bucket.puts.Load() != 1 {
		t.Fatalf("Bucket Put count = %d, want 1", rig.bucket.puts.Load())
	}
}

// TestConcurrentCompletionDelivery invokes the same durable receipt from many
// callers; the callback and acknowledgement must happen once without races.
func TestConcurrentCompletionDelivery(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	ctx := context.Background()
	record := newCompletionRecord(CompletedUpload{ID: "concurrent", Key: "uploads/concurrent"})
	if err = scratch.SaveCompletion(ctx, record); err != nil {
		t.Fatalf("SaveCompletion: %v", err)
	}
	h := &Handler{scratch: scratch, log: slog.New(slog.DiscardHandler)}
	var calls atomic.Int32
	mustOnComplete(t, h, "concurrent", func(context.Context, CompletedUpload) error {
		calls.Add(1)
		return nil
	})

	const callers = 32
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			<-start
			errs <- h.deliverCompletion(ctx, "concurrent")
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for deliveryErr := range errs {
		if deliveryErr != nil {
			t.Fatalf("deliverCompletion: %v", deliveryErr)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("callback count = %d, want 1", calls.Load())
	}
}

// TestFinishUploadRetrySkipsPersistedObject verifies a direct tusd retry uses
// the durable receipt instead of writing the same object a second time.
func TestFinishUploadRetrySkipsPersistedObject(t *testing.T) {
	t.Parallel()
	local, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	bucket := &putCountingBucket{Bucket: local}
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	h := &Handler{scratch: scratch, log: slog.New(slog.DiscardHandler)}
	var callbacks atomic.Int32
	mustOnComplete(t, h, "finish-retry", func(context.Context, CompletedUpload) error {
		if callbacks.Add(1) == 1 {
			return errBoom
		}
		return nil
	})
	store := newBucketStore(
		scratch, bucket, defaultKeyFunc("uploads/"), h.log, h.deliverCompletion,
	)
	ctx := context.Background()
	upload, err := store.NewUpload(ctx, tusd.FileInfo{Size: 4})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if _, err = upload.WriteChunk(ctx, 0, strings.NewReader("data")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	if err = upload.FinishUpload(ctx); err == nil {
		t.Fatal("first FinishUpload should surface callback failure")
	}
	if err = upload.FinishUpload(ctx); err != nil {
		t.Fatalf("retry FinishUpload: %v", err)
	}
	if bucket.puts.Load() != 1 || callbacks.Load() != 2 {
		t.Fatalf("puts=%d callbacks=%d, want puts=1 callbacks=2", bucket.puts.Load(), callbacks.Load())
	}
}

func TestFinishUpload_PrePutCheckpointFailuresPreventPut(t *testing.T) {
	t.Parallel()
	for _, failAt := range []int32{1, 2} {
		t.Run(strconv.Itoa(int(failAt)), func(t *testing.T) {
			t.Parallel()
			assertPrePutCheckpointFailure(t, failAt)
		})
	}
}

func assertPrePutCheckpointFailure(t *testing.T, failAt int32) {
	t.Helper()
	local, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	bucket := &putCountingBucket{Bucket: local}
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	failing := &failCompletionSaveScratch{scratchStore: scratch, failAt: failAt}
	store := newBucketStore(failing, bucket, defaultKeyFunc("uploads/"), slog.New(slog.DiscardHandler), nil)
	upload, err := store.NewUpload(context.Background(), tusd.FileInfo{Size: 4})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if _, err = upload.WriteChunk(context.Background(), 0, strings.NewReader("data")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	if err = upload.FinishUpload(context.Background()); err == nil {
		t.Fatal("FinishUpload should fail before Put")
	}
	if bucket.puts.Load() != 0 {
		t.Fatalf("Bucket Put count = %d, want 0", bucket.puts.Load())
	}
}

func TestCompletionCheckpointFailureReconcilesWithoutDuplicatePut(t *testing.T) {
	t.Parallel()
	local, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	bucket := &putCountingBucket{Bucket: local}
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	failing := &failCompletionSaveScratch{scratchStore: scratch, failAt: 3}
	store := newBucketStore(failing, bucket, defaultKeyFunc("uploads/"), slog.New(slog.DiscardHandler), nil)
	upload, err := store.NewUpload(context.Background(), tusd.FileInfo{Size: 4})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if _, err = upload.WriteChunk(context.Background(), 0, strings.NewReader("data")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	info, err := upload.GetInfo(context.Background())
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if err = upload.FinishUpload(context.Background()); err == nil {
		t.Fatal("FinishUpload should surface persisted-stage checkpoint failure")
	}
	record, err := scratch.Completion(context.Background(), info.ID)
	if err != nil || record.normalizedStage() != completionStagePutting {
		t.Fatalf("putting receipt = %+v, err=%v", record, err)
	}
	restarted := newBucketStore(failing, bucket, defaultKeyFunc("uploads/"), slog.New(slog.DiscardHandler), nil)
	if err = restarted.ResumeCompletion(context.Background(), info.ID); err != nil {
		t.Fatalf("ResumeCompletion: %v", err)
	}
	if bucket.puts.Load() != 1 {
		t.Fatalf("Bucket Put count = %d, want 1", bucket.puts.Load())
	}
}

func TestCompletionRetryRejectsChangedScratch(t *testing.T) {
	t.Parallel()
	local, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	bucket := &putCountingBucket{Bucket: local}
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	failing := &failCompletionSaveScratch{scratchStore: scratch, failAt: 3}
	store := newBucketStore(failing, bucket, defaultKeyFunc("uploads/"), slog.New(slog.DiscardHandler), nil)
	upload, err := store.NewUpload(context.Background(), tusd.FileInfo{Size: 4})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if _, err = upload.WriteChunk(context.Background(), 0, strings.NewReader("data")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	info, err := upload.GetInfo(context.Background())
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if err = upload.FinishUpload(context.Background()); err == nil {
		t.Fatal("FinishUpload should surface persisted-stage checkpoint failure")
	}
	binPath, err := scratch.binPath(info.ID)
	if err != nil {
		t.Fatalf("binPath: %v", err)
	}
	if err = os.WriteFile(binPath, []byte("evil"), scratchFilePerm); err != nil {
		t.Fatalf("replace scratch bytes: %v", err)
	}
	restarted := newBucketStore(failing, bucket, defaultKeyFunc("uploads/"), slog.New(slog.DiscardHandler), nil)
	if err = restarted.ResumeCompletion(context.Background(), info.ID); err == nil {
		t.Fatal("ResumeCompletion accepted scratch bytes changed after checkpoint")
	}
	if bucket.puts.Load() != 1 {
		t.Fatalf("Bucket Put count = %d, want 1", bucket.puts.Load())
	}
	rc, _, err := local.Get(context.Background(), "uploads/"+info.ID)
	if err != nil {
		t.Fatalf("Get persisted object: %v", err)
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read persisted object: %v", err)
	}
	if string(body) != "data" {
		t.Fatalf("persisted object = %q; want checkpointed bytes", body)
	}
}

func TestAmbiguousPutResultReconcilesWithoutDuplicatePut(t *testing.T) {
	t.Parallel()
	local, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	bucket := &errorAfterPutBucket{Bucket: local}
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	store := newBucketStore(scratch, bucket, defaultKeyFunc("uploads/"), slog.New(slog.DiscardHandler), nil)
	upload, err := store.NewUpload(context.Background(), tusd.FileInfo{Size: 4})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if _, err = upload.WriteChunk(context.Background(), 0, strings.NewReader("data")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	info, err := upload.GetInfo(context.Background())
	if err != nil {
		t.Fatalf("GetInfo: %v", err)
	}
	if err = upload.FinishUpload(context.Background()); err == nil {
		t.Fatal("FinishUpload should surface ambiguous Put result")
	}
	if err = store.ResumeCompletion(context.Background(), info.ID); err != nil {
		t.Fatalf("ResumeCompletion: %v", err)
	}
	if bucket.puts.Load() != 1 {
		t.Fatalf("Bucket Put count = %d, want 1", bucket.puts.Load())
	}
}

func TestPreparedCompletionCannotRunCallbacks(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	digest := sha256.Sum256(nil)
	record := newPreparedCompletionRecord(
		CompletedUpload{ID: "prepared", Key: "uploads/prepared"},
		hex.EncodeToString(digest[:]),
	)
	if err = scratch.SaveCompletion(context.Background(), record); err != nil {
		t.Fatalf("SaveCompletion: %v", err)
	}
	h := &Handler{scratch: scratch, log: slog.New(slog.DiscardHandler)}
	var calls atomic.Int32
	mustOnComplete(t, h, "prepared", func(context.Context, CompletedUpload) error {
		calls.Add(1)
		return nil
	})
	if err = h.deliverCompletion(context.Background(), "prepared"); err == nil {
		t.Fatal("prepared completion should not be delivered")
	}
	if calls.Load() != 0 {
		t.Fatalf("callback calls = %d, want 0", calls.Load())
	}
}

func TestDestinationMismatchIsOverwritten(t *testing.T) {
	t.Parallel()
	for _, prior := range []string{"x", "xxxx"} {
		t.Run(strconv.Itoa(len(prior)), func(t *testing.T) {
			t.Parallel()
			local, err := storage.NewLocalBucket(t.TempDir())
			if err != nil {
				t.Fatalf("NewLocalBucket: %v", err)
			}
			bucket := &putCountingBucket{Bucket: local}
			scratch, err := newLocalScratch(t.TempDir())
			if err != nil {
				t.Fatalf("newLocalScratch: %v", err)
			}
			store := newBucketStore(scratch, bucket, defaultKeyFunc("uploads/"), slog.New(slog.DiscardHandler), nil)
			upload, err := store.NewUpload(context.Background(), tusd.FileInfo{Size: 4})
			if err != nil {
				t.Fatalf("NewUpload: %v", err)
			}
			if _, err = upload.WriteChunk(context.Background(), 0, strings.NewReader("data")); err != nil {
				t.Fatalf("WriteChunk: %v", err)
			}
			info, infoErr := upload.GetInfo(context.Background())
			if infoErr != nil {
				t.Fatalf("GetInfo: %v", infoErr)
			}
			if _, err = local.Put(context.Background(), "uploads/"+info.ID, strings.NewReader(prior), storage.PutOptions{}); err != nil {
				t.Fatalf("seed destination: %v", err)
			}
			if err = upload.FinishUpload(context.Background()); err != nil {
				t.Fatalf("FinishUpload: %v", err)
			}
			if bucket.puts.Load() != 1 {
				t.Fatalf("Bucket Put count = %d, want 1", bucket.puts.Load())
			}
		})
	}
}

func TestDestinationAttributeMismatchIsOverwritten(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		opts storage.PutOptions
	}{
		{name: "content type", opts: storage.PutOptions{ContentType: "text/stale", Metadata: map[string]string{"filetype": "text/plain", "owner": "alice"}}},
		{name: "metadata", opts: storage.PutOptions{ContentType: "text/plain", Metadata: map[string]string{"filetype": "text/plain", "owner": "stale"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			local, err := storage.NewLocalBucket(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			bucket := &putCountingBucket{Bucket: local}
			scratch, err := newLocalScratch(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			store := newBucketStore(scratch, bucket, defaultKeyFunc("uploads/"), slog.New(slog.DiscardHandler), nil)
			info := tusd.FileInfo{Size: 4, MetaData: tusd.MetaData{"filetype": "text/plain", "owner": "alice"}}
			upload, err := store.NewUpload(context.Background(), info)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = upload.WriteChunk(context.Background(), 0, strings.NewReader("data")); err != nil {
				t.Fatal(err)
			}
			storedInfo, err := upload.GetInfo(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			key := "uploads/" + storedInfo.ID
			if _, err = local.Put(context.Background(), key, strings.NewReader("data"), tc.opts); err != nil {
				t.Fatal(err)
			}
			if err = upload.FinishUpload(context.Background()); err != nil {
				t.Fatal(err)
			}
			if bucket.puts.Load() != 1 {
				t.Fatalf("Bucket Put count = %d, want 1", bucket.puts.Load())
			}
			obj, err := local.Stat(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			if obj.ContentType != "text/plain" || obj.Metadata["owner"] != "alice" {
				t.Fatalf("reconciled object = %+v", obj)
			}
		})
	}
}

func TestDestinationProbeFailureDoesNotRiskPut(t *testing.T) {
	t.Parallel()
	local, err := storage.NewLocalBucket(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalBucket: %v", err)
	}
	counting := &putCountingBucket{Bucket: local}
	bucket := probeFailBucket{putCountingBucket: counting}
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	store := newBucketStore(scratch, bucket, defaultKeyFunc("uploads/"), slog.New(slog.DiscardHandler), nil)
	upload, err := store.NewUpload(context.Background(), tusd.FileInfo{Size: 4})
	if err != nil {
		t.Fatalf("NewUpload: %v", err)
	}
	if _, err = upload.WriteChunk(context.Background(), 0, strings.NewReader("data")); err != nil {
		t.Fatalf("WriteChunk: %v", err)
	}
	if err = upload.FinishUpload(context.Background()); err == nil {
		t.Fatal("FinishUpload should fail closed when destination cannot be probed")
	}
	if counting.puts.Load() != 0 {
		t.Fatalf("Bucket Put count = %d, want 0", counting.puts.Load())
	}
}

func TestS3CompletionReceiptRecordsUploadedSize(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<Error><Code>NoSuchKey</Code></Error>`))
		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read S3 body: %v", err)
			}
			if string(body) != "data" {
				t.Errorf("S3 body = %q; want data", body)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected S3 method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	bucket, err := storage.NewS3Bucket(context.Background(), storage.S3Options{
		Bucket: "uploads", Region: "us-east-1", Endpoint: srv.URL,
		AccessKey: "ak", SecretKey: "sk", PathStyle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := newBucketStore(
		scratch, bucket, defaultKeyFunc("objects/"), slog.New(slog.DiscardHandler),
		func(context.Context, string) error { return nil },
	)
	upload, err := store.NewUpload(context.Background(), tusd.FileInfo{Size: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = upload.WriteChunk(context.Background(), 0, strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	info, err := upload.GetInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = upload.FinishUpload(context.Background()); err != nil {
		t.Fatal(err)
	}
	record, err := scratch.Completion(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Upload.Size != 4 {
		t.Fatalf("completion size = %d; want 4", record.Upload.Size)
	}
}

func TestResumePersistedCompletionWithoutScratch(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	record := newCompletionRecord(CompletedUpload{ID: "persisted", Key: "uploads/persisted"})
	if err = scratch.SaveCompletion(context.Background(), record); err != nil {
		t.Fatalf("SaveCompletion: %v", err)
	}
	h := &Handler{scratch: scratch, log: slog.New(slog.DiscardHandler)}
	var calls atomic.Int32
	mustOnComplete(t, h, "resume", func(context.Context, CompletedUpload) error {
		calls.Add(1)
		return nil
	})
	store := newBucketStore(scratch, nil, defaultKeyFunc("uploads/"), h.log, h.deliverCompletion)
	if err = store.ResumeCompletion(context.Background(), "persisted"); err != nil {
		t.Fatalf("ResumeCompletion: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("callback calls = %d, want 1", calls.Load())
	}
}
