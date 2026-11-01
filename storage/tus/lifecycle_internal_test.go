// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	tusd "github.com/tus/tusd/v2/pkg/handler"
)

// agePastMtime backdates an upload's scratch files so the sweep treats it as
// expired. Expiry is filesystem-mtime driven, so the test ages the files
// rather than the clock.
func agePastMtime(t *testing.T, root, id string, by time.Duration) {
	t.Helper()
	past := time.Now().Add(-by)
	for _, suffix := range []string{"", ".info"} {
		p := filepath.Join(root, id+suffix)
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatalf("chtimes %s: %v", p, err)
		}
	}
}

// TestSweepExpired drives the OnStop expiry sweep: an in-progress upload whose
// scratch files predate UploadExpiry is removed; a fresh one is kept.
func TestSweepExpired(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	ctx := context.Background()
	if _, err = scratch.Create(ctx, tusd.FileInfo{ID: "stale"}); err != nil {
		t.Fatalf("create stale: %v", err)
	}
	if _, err = scratch.Create(ctx, tusd.FileInfo{ID: "fresh"}); err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	if _, err = scratch.Create(ctx, tusd.FileInfo{ID: "pending"}); err != nil {
		t.Fatalf("create pending: %v", err)
	}
	agePastMtime(t, scratch.root, "stale", 48*time.Hour)
	agePastMtime(t, scratch.root, "pending", 48*time.Hour)
	if err = scratch.SaveCompletion(ctx, newCompletionRecord(CompletedUpload{
		ID: "pending", Key: "uploads/pending",
	})); err != nil {
		t.Fatalf("save pending completion: %v", err)
	}

	h := &Handler{
		scratch: scratch,
		log:     slog.New(slog.DiscardHandler),
		clk:     clockwork.NewRealClock(), // sweep compares against wall-clock mtimes
		opts:    Options{UploadExpiry: time.Hour},
	}
	h.sweepExpired(ctx)

	if _, err = scratch.Get(ctx, "stale"); err == nil {
		t.Fatal("stale upload should have been swept")
	}
	if _, err = scratch.Get(ctx, "fresh"); err != nil {
		t.Fatalf("fresh upload should remain: %v", err)
	}
	if _, err = scratch.Get(ctx, "pending"); err != nil {
		t.Fatalf("upload with pending completion should remain: %v", err)
	}
}

func seedMaintenanceUploads(t *testing.T, scratch *localScratch, ctx context.Context, total int) {
	t.Helper()
	for i := range total {
		id := fmt.Sprintf("expired-%03d", i)
		if _, err := scratch.Create(ctx, tusd.FileInfo{ID: id}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		agePastMtime(t, scratch.root, id, 48*time.Hour)
		completionID := fmt.Sprintf("complete-%03d", i)
		if err := scratch.SaveCompletion(ctx, newCompletionRecord(CompletedUpload{ID: completionID})); err != nil {
			t.Fatalf("save completion %s: %v", completionID, err)
		}
	}
}

func scanExpiryBatch(t *testing.T, scratch *localScratch, ctx context.Context, expired map[string]struct{}) {
	t.Helper()
	ids, expiryErr := scratch.Expired(ctx, time.Now(), time.Hour)
	if expiryErr != nil {
		t.Fatalf("Expired: %v", expiryErr)
	}
	if len(ids) > maxMaintenanceBatch {
		t.Fatalf("expiry batch = %d", len(ids))
	}
	for _, id := range ids {
		expired[id] = struct{}{}
	}
}

func scanCompletionBatch(t *testing.T, scratch *localScratch, ctx context.Context, completed map[string]struct{}) {
	t.Helper()
	ids, completionErr := scratch.CompletionIDs(ctx, maxMaintenanceBatch)
	if completionErr != nil {
		t.Fatalf("CompletionIDs: %v", completionErr)
	}
	if len(ids) > maxMaintenanceBatch {
		t.Fatalf("completion batch = %d", len(ids))
	}
	for _, id := range ids {
		completed[id] = struct{}{}
	}
}

func collectMaintenanceBatches(t *testing.T, scratch *localScratch, ctx context.Context, total int) (map[string]struct{}, map[string]struct{}) {
	t.Helper()
	expired := make(map[string]struct{}, total)
	completed := make(map[string]struct{}, total)
	for range 8 {
		scanExpiryBatch(t, scratch, ctx, expired)
		scanCompletionBatch(t, scratch, ctx, completed)
		if len(expired) == total && len(completed) == total {
			break
		}
	}
	return expired, completed
}

func TestLocalScratch_MaintenanceScansAreBoundedAndAdvance(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	t.Cleanup(func() { _ = scratch.Close() })
	ctx := context.Background()
	const total = maxMaintenanceBatch + 44
	seedMaintenanceUploads(t, scratch, ctx, total)

	expired, completed := collectMaintenanceBatches(t, scratch, ctx, total)
	if len(expired) != total || len(completed) != total {
		t.Fatalf("cursor coverage: expired=%d completed=%d want=%d", len(expired), len(completed), total)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = scratch.CompletionIDs(canceled, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled CompletionIDs error = %v", err)
	}
}

// TestDirCursor_ReleasesHandleAtDirectoryEnd verifies a pass that reaches the
// end of the directory holds no handle; Windows refuses to remove a directory
// with an open handle.
func TestDirCursor_ReleasesHandleAtDirectoryEnd(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	ctx := context.Background()
	cases := []struct {
		name     string
		limit    int
		wantOpen bool
	}{
		{name: "short batch reaches end", limit: 3, wantOpen: false},
		{name: "full batch may continue", limit: 2, wantOpen: true},
		{name: "partial batch continues", limit: 1, wantOpen: true},
	}
	for _, tc := range cases {
		var c dirCursor
		entries, err := c.read(ctx, root, tc.limit)
		if err != nil || len(entries) != min(tc.limit, 2) {
			t.Fatalf("%s: read = %d entries, %v", tc.name, len(entries), err)
		}
		if open := c.dir != nil; open != tc.wantOpen {
			t.Errorf("%s: handle open = %t, want %t", tc.name, open, tc.wantOpen)
		}
		if err = c.close(); err != nil {
			t.Fatalf("%s: close: %v", tc.name, err)
		}
	}
}

// TestLocalScratch_CompletionScanRestartsAtDirectoryEnd verifies a pass after
// one that reached directory end sees entries created in between (#689).
func TestLocalScratch_CompletionScanRestartsAtDirectoryEnd(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	t.Cleanup(func() { _ = scratch.Close() })
	ctx := context.Background()
	if _, err = scratch.Create(ctx, tusd.FileInfo{ID: "pending"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertCompletionIDs(t, scratch, maxMaintenanceBatch) // short batch parks the handle at end
	if err = scratch.SaveCompletion(ctx, newCompletionRecord(CompletedUpload{ID: "pending"})); err != nil {
		t.Fatalf("SaveCompletion: %v", err)
	}
	assertCompletionIDs(t, scratch, maxMaintenanceBatch, "pending")
	if err = scratch.RemoveUpload(ctx, "pending"); err != nil {
		t.Fatalf("RemoveUpload: %v", err)
	}
	// Boundary: a batch filled exactly at directory end must not yield an empty pass.
	assertCompletionIDs(t, scratch, 1, "pending")
	assertCompletionIDs(t, scratch, 1, "pending")
	if err = scratch.DeleteCompletion(ctx, "pending"); err != nil {
		t.Fatalf("DeleteCompletion: %v", err)
	}
	assertCompletionIDs(t, scratch, 1)
}

func assertCompletionIDs(t *testing.T, scratch *localScratch, limit int, want ...string) {
	t.Helper()
	got, err := scratch.CompletionIDs(context.Background(), limit)
	if err != nil {
		t.Fatalf("CompletionIDs: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("CompletionIDs(limit=%d) = %v, want %v", limit, got, want)
	}
}

// TestLocalScratch_ExpiryScanRestartsAtDirectoryEnd verifies the expiry sweep
// sees an upload that expired after the previous pass reached directory end.
func TestLocalScratch_ExpiryScanRestartsAtDirectoryEnd(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	t.Cleanup(func() { _ = scratch.Close() })
	ctx := context.Background()
	if _, err = scratch.Create(ctx, tusd.FileInfo{ID: "fresh"}); err != nil {
		t.Fatalf("Create fresh: %v", err)
	}
	ids, err := scratch.Expired(ctx, time.Now(), time.Hour)
	if err != nil || len(ids) != 0 {
		t.Fatalf("first Expired = %v, %v; want none", ids, err)
	}
	if _, err = scratch.Create(ctx, tusd.FileInfo{ID: "stale"}); err != nil {
		t.Fatalf("Create stale: %v", err)
	}
	agePastMtime(t, scratch.root, "stale", 48*time.Hour)
	ids, err = scratch.Expired(ctx, time.Now(), time.Hour)
	if err != nil || !slices.Equal(ids, []string{"stale"}) {
		t.Fatalf("second Expired = %v, %v; want [stale]", ids, err)
	}
}

func startAsyncPatch(t *testing.T, server *httptest.Server, location string) (*io.PipeWriter, <-chan int, <-chan error) {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	req, err := http.NewRequest(http.MethodPatch, location, reader)
	if err != nil {
		t.Fatalf("new PATCH: %v", err)
	}
	req.ContentLength = 4
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Offset", "0")
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	statusCodes := make(chan int, 1)
	requestErrs := make(chan error, 1)
	go func() {
		resp, requestErr := server.Client().Do(req)
		if requestErr != nil {
			requestErrs <- requestErr
			return
		}
		defer func() { _ = resp.Body.Close() }()
		statusCodes <- resp.StatusCode
	}()
	return writer, statusCodes, requestErrs
}

func waitForMaintenanceLock(t *testing.T, h *Handler, id string, statusCodes <-chan int, requestErrs <-chan error) {
	t.Helper()
	busy := false
	for range 3000 {
		select {
		case requestErr := <-requestErrs:
			t.Fatalf("PATCH before lock observation: %v", requestErr)
		case status := <-statusCodes:
			t.Fatalf("PATCH returned before body release with status %d", status)
		default:
		}
		unlock, ok := h.locker.tryMaintenanceLock(id)
		if !ok {
			busy = true
			break
		}
		unlock()
		time.Sleep(time.Millisecond)
	}
	if !busy {
		t.Fatal("PATCH did not acquire the shared upload lock")
	}
}

func completePatchAndVerify(t *testing.T, writer *io.PipeWriter, statusCodes <-chan int, requestErrs <-chan error) {
	t.Helper()
	if _, err := writer.Write([]byte("data")); err != nil {
		t.Fatalf("write PATCH body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close PATCH body: %v", err)
	}
	select {
	case requestErr := <-requestErrs:
		t.Fatalf("PATCH: %v", requestErr)
	case status := <-statusCodes:
		if status != http.StatusNoContent {
			t.Fatalf("PATCH status = %d, want 204", status)
		}
	case <-time.After(time.Second):
		t.Fatal("PATCH did not finish")
	}
}

func TestSweepExpired_SkipsActivePatchAndRechecksActivity(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	opts.Enabled = true
	opts.UploadExpiry = time.Hour
	opts.GracefulRequestCompletionTimeout = time.Millisecond
	p, _ := newTestParams(t, opts)
	h, err := newHandler(p)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	location := createTestUpload(t, server.Client(), server.URL+h.BasePath(), 8)
	id := path.Base(location)
	agePastMtime(t, p.Opts.ScratchDir, id, 2*time.Hour)

	writer, statusCodes, requestErrs := startAsyncPatch(t, server, location)
	waitForMaintenanceLock(t, h, id, statusCodes, requestErrs)

	h.sweepExpired(context.Background())
	if _, err = h.scratch.Get(context.Background(), id); err != nil {
		t.Fatalf("active PATCH scratch was swept: %v", err)
	}
	completePatchAndVerify(t, writer, statusCodes, requestErrs)

	h.sweepExpired(context.Background())
	if _, err = h.scratch.Get(context.Background(), id); err != nil {
		t.Fatalf("recent PATCH activity was not retained: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
}

// TestLifecycle_PeriodicallySweepsExpired verifies expiry runs while the app
// is live instead of waiting until shutdown.
func TestLifecycle_PeriodicallySweepsExpired(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	opts.Enabled = true
	opts.UploadExpiry = time.Hour
	p, lc := newTestParams(t, opts)
	fakeClock, ok := p.Clock.(*clockwork.FakeClock)
	if !ok {
		t.Fatalf("clock type = %T, want *clockwork.FakeClock", p.Clock)
	}
	h, err := newHandler(p)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	ctx := context.Background()
	if _, err = h.scratch.Create(ctx, tusd.FileInfo{ID: "runtime-stale"}); err != nil {
		t.Fatalf("create stale: %v", err)
	}
	agePastMtime(t, p.Opts.ScratchDir, "runtime-stale", 2*time.Hour)
	if err = lc.Start(ctx); err != nil {
		t.Fatalf("lifecycle start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if stopErr := lc.Stop(stopCtx); stopErr != nil {
			t.Errorf("lifecycle stop: %v", stopErr)
		}
	})
	waitCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err = fakeClock.BlockUntilContext(waitCtx, 1); err != nil {
		t.Fatalf("periodic sweep ticker did not start: %v", err)
	}
	fakeClock.Advance(15 * time.Minute)

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for range 100 {
		if _, getErr := h.scratch.Get(ctx, "runtime-stale"); getErr != nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("expired scratch survived runtime sweep")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	t.Fatal("expired scratch survived runtime sweep")
}

// TestExpiredListing checks the scratch Expired predicate against a TTL window.
func TestExpiredListing(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	ctx := context.Background()
	if _, err = scratch.Create(ctx, tusd.FileInfo{ID: "a"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// now far in the future, ttl small => the entry is expired.
	ids, err := scratch.Expired(ctx, time.Now().Add(48*time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("Expired: %v", err)
	}
	if len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("expired ids = %v, want [a]", ids)
	}

	// ttl large => nothing expired.
	ids, err = scratch.Expired(ctx, time.Now(), 72*time.Hour)
	if err != nil {
		t.Fatalf("Expired: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expired ids = %v, want none", ids)
	}
}

// TestWaitDrain_DeadlineBranch covers the path where the drain goroutine has
// not finished before the OnStop context expires.
func TestWaitDrain_DeadlineBranch(t *testing.T) {
	t.Parallel()
	h := &Handler{
		log:       slog.New(slog.DiscardHandler),
		drainDone: make(chan struct{}), // never closed
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done
	if err := h.waitDrain(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitDrain error = %v; want context.Canceled", err)
	}
}

func TestStopLeavesScratchUntouchedWhenDrainJoinTimesOut(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	h := &Handler{
		scratch:   scratch,
		log:       slog.New(slog.DiscardHandler),
		drainDone: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = h.stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("stop error = %v; want context.Canceled", err)
	}
	if _, err = os.Stat(scratch.root); err != nil {
		t.Fatalf("scratch root changed after failed join: %v", err)
	}
	if _, err = scratch.Create(context.Background(), tusd.FileInfo{ID: "still-open", Size: 1}); err != nil {
		t.Fatalf("scratch unusable after failed join: %v", err)
	}
}

// blockingCompletionScratch lists one durable completion and blocks loading it
// until released, like a file call that ignores ctx and outlives the stop deadline.
type blockingCompletionScratch struct {
	scratchStore
	entered chan struct{}
	release chan struct{}
}

func (s *blockingCompletionScratch) CompletionIDs(context.Context, int) ([]string, error) {
	return []string{"stuck-upload"}, nil
}

func (s *blockingCompletionScratch) Completion(context.Context, string) (completionRecord, error) {
	close(s.entered)
	<-s.release
	return completionRecord{}, tusd.ErrNotFound
}

// TestStopTimeoutNamesDrainPhase pins #736: when the drain outlives the stop
// deadline, the error names the operation and upload it is blocked in.
func TestStopTimeoutNamesDrainPhase(t *testing.T) {
	t.Parallel()
	local, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	scratch := &blockingCompletionScratch{scratchStore: local, entered: make(chan struct{}), release: make(chan struct{})}
	h := &Handler{
		unrouted:  &tusd.UnroutedHandler{CompleteUploads: make(chan tusd.HookEvent)},
		scratch:   scratch,
		log:       slog.New(slog.DiscardHandler),
		clk:       clockwork.NewRealClock(),
		opts:      Options{ExpirySweepInterval: time.Hour},
		drainDone: make(chan struct{}),
	}
	drainCtx, cancel := context.WithCancel(context.Background())
	h.drainCancel = cancel
	go h.drainCompletions(drainCtx)
	select {
	case <-scratch.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("drain never loaded the listed completion")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stopCancel()
	err = h.stop(stopCtx)
	close(scratch.release)
	select {
	case <-h.drainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not exit after release")
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "complete upload stuck-upload") {
		t.Fatalf("stop error = %v; want deadline exceeded naming the blocked upload", err)
	}
}

// TestDrainCompletions_ClosedChannelReturns ensures an unexpected upstream
// channel close cannot turn the lifecycle worker into a zero-value busy loop.
func TestDrainCompletions_ClosedChannelReturns(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	completeUploads := make(chan tusd.HookEvent)
	close(completeUploads)
	h := &Handler{
		unrouted: &tusd.UnroutedHandler{CompleteUploads: completeUploads},
		scratch:  scratch,
		log:      slog.New(slog.DiscardHandler),
		clk:      clockwork.NewRealClock(),
		opts: Options{
			ExpirySweepInterval: time.Hour,
		},
		drainDone: make(chan struct{}),
	}

	go h.drainCompletions(context.Background())
	select {
	case <-h.drainDone:
	case <-time.After(time.Second):
		t.Fatal("drain worker did not return after notification channel closed")
	}
}

func TestOnCompleteRejectsInvalidRegistration(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	callback := func(context.Context, CompletedUpload) error { return nil }
	if err := h.OnComplete("", callback); err == nil {
		t.Fatal("empty callback id accepted")
	}
	if err := h.OnComplete("nil", nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	if err := h.OnComplete("stable", callback); err != nil {
		t.Fatalf("register stable callback: %v", err)
	}
	if err := h.OnComplete("stable", callback); err == nil {
		t.Fatal("duplicate callback id accepted")
	}
}

func testRunCallbacksInitialFailure(t *testing.T, scratch *localScratch, record *completionRecord, first, second *int) {
	t.Helper()
	firstHandler := &Handler{scratch: scratch, log: slog.New(slog.DiscardHandler)}
	if err := firstHandler.OnComplete("first", func(context.Context, CompletedUpload) error {
		*first++
		return nil
	}); err != nil {
		t.Fatalf("register first callback: %v", err)
	}
	if err := firstHandler.OnComplete("second", func(context.Context, CompletedUpload) error {
		*second++
		return errBoom
	}); err != nil {
		t.Fatalf("register second callback: %v", err)
	}
	if err := firstHandler.runCallbacks(context.Background(), record); err == nil {
		t.Fatal("expected error from first callback")
	}
	if *first != 1 || *second != 1 || !slices.Equal(record.CompletedCallbackIDs, []string{"first"}) {
		t.Fatalf("after failure: first=%d second=%d completed=%v", *first, *second, record.CompletedCallbackIDs)
	}
}

func testRunCallbacksReorderedRetry(t *testing.T, scratch *localScratch, first, second, inserted *int) {
	t.Helper()
	reordered := &Handler{scratch: scratch, log: slog.New(slog.DiscardHandler)}
	if err := reordered.OnComplete("second", func(context.Context, CompletedUpload) error {
		*second++
		return nil
	}); err != nil {
		t.Fatalf("register reordered second callback: %v", err)
	}
	if err := reordered.OnComplete("inserted", func(context.Context, CompletedUpload) error {
		*inserted++
		return nil
	}); err != nil {
		t.Fatalf("register inserted callback: %v", err)
	}
	if err := reordered.OnComplete("first", func(context.Context, CompletedUpload) error {
		*first++
		return nil
	}); err != nil {
		t.Fatalf("register reordered first callback: %v", err)
	}
	record, err := scratch.Completion(context.Background(), "checkpoint")
	if err != nil {
		t.Fatalf("Completion: %v", err)
	}
	if err = reordered.runCallbacks(context.Background(), &record); err != nil {
		t.Fatalf("retry callbacks: %v", err)
	}
	wantCompleted := []string{"first", "second", "inserted"}
	if *first != 1 || *second != 2 || *inserted != 1 || !slices.Equal(record.CompletedCallbackIDs, wantCompleted) {
		t.Fatalf("after retry: first=%d second=%d inserted=%d completed=%v", *first, *second, *inserted, record.CompletedCallbackIDs)
	}
}

// TestRunCallbacks_CheckpointsStableIDs ensures deployment-time callback
// reordering and insertion cannot replay an acknowledged callback or skip a new one.
func TestRunCallbacks_CheckpointsStableIDs(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	record := newCompletionRecord(CompletedUpload{ID: "checkpoint", Key: "uploads/checkpoint"})
	if err = scratch.SaveCompletion(context.Background(), record); err != nil {
		t.Fatalf("SaveCompletion: %v", err)
	}
	var first, second, inserted int
	testRunCallbacksInitialFailure(t, scratch, &record, &first, &second)
	testRunCallbacksReorderedRetry(t, scratch, &first, &second, &inserted)
}

func testCompletionMetadataCallbackIsolation(t *testing.T, scratch *localScratch, record *completionRecord) {
	t.Helper()
	h := &Handler{scratch: scratch, log: slog.New(slog.DiscardHandler)}
	var retained map[string]string
	if err := h.OnComplete("mutator", func(_ context.Context, upload CompletedUpload) error {
		retained = upload.MetaData
		upload.MetaData["owner"] = "callback-mutated"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.OnComplete("observer", func(_ context.Context, upload CompletedUpload) error {
		if upload.MetaData["owner"] != "alice" {
			t.Fatalf("later callback metadata = %v; want isolated original", upload.MetaData)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.runCallbacks(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	retained["after-return"] = "mutation"
	if record.Upload.MetaData["owner"] != "alice" || record.Upload.MetaData["after-return"] != "" {
		t.Fatalf("record metadata aliased callback payload: %v", record.Upload.MetaData)
	}
	persisted, err := scratch.Completion(context.Background(), "metadata-clone")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Upload.MetaData["owner"] != "alice" || persisted.Upload.MetaData["after-return"] != "" {
		t.Fatalf("persisted metadata aliased callback payload: %v", persisted.Upload.MetaData)
	}
}

func TestCompletionMetadataIsClonedAcrossDurableAndCallbackBoundaries(t *testing.T) {
	t.Parallel()
	input := map[string]string{"owner": "alice"}
	record := newCompletionRecord(CompletedUpload{
		ID: "metadata-clone", Key: "uploads/metadata-clone", MetaData: input,
	})
	input["owner"] = "input-mutated"
	if record.Upload.MetaData["owner"] != "alice" {
		t.Fatalf("record metadata changed through constructor input: %v", record.Upload.MetaData)
	}

	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = scratch.SaveCompletion(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	testCompletionMetadataCallbackIsolation(t, scratch, &record)
}

func TestCompletionRecordRejectsUnversionedState(t *testing.T) {
	t.Parallel()
	scratch, err := newLocalScratch(t.TempDir())
	if err != nil {
		t.Fatalf("newLocalScratch: %v", err)
	}
	legacy := completionRecord{
		Stage:  completionStagePersisted,
		Upload: completionUpload{ID: "legacy", Key: "uploads/legacy"},
	}
	if err = scratch.SaveCompletion(context.Background(), legacy); err == nil {
		t.Fatal("SaveCompletion accepted unversioned record")
	}
	completionPath, err := scratch.completionPath("legacy")
	if err != nil {
		t.Fatalf("completionPath: %v", err)
	}
	data := []byte(`{"stage":"persisted","upload":{"id":"legacy","key":"uploads/legacy"}}`)
	if err = os.WriteFile(completionPath, data, scratchFilePerm); err != nil {
		t.Fatalf("write legacy completion: %v", err)
	}
	if _, err = scratch.Completion(context.Background(), "legacy"); err == nil {
		t.Fatal("Completion accepted unversioned record")
	}
}

var errBoom = errSentinel("boom")

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// TestDefaultOptions pins the documented defaults.
func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	o := defaultOptions()
	switch {
	case o.Enabled:
		t.Error("Enabled should default false (opt-in)")
	case o.BasePath != "/files/":
		t.Errorf("BasePath = %q", o.BasePath)
	case o.MaxSize != defaultMaxSize:
		t.Errorf("MaxSize = %d", o.MaxSize)
	case o.KeyPrefix != "uploads/":
		t.Errorf("KeyPrefix = %q", o.KeyPrefix)
	case o.Scratch != "local":
		t.Errorf("Scratch = %q", o.Scratch)
	case !o.DisableDownload:
		t.Error("DisableDownload should default true")
	case !o.DisableConcatenation:
		t.Error("DisableConcatenation should default true")
	case o.UploadExpiry != 24*time.Hour:
		t.Errorf("UploadExpiry = %v", o.UploadExpiry)
	case o.ExpirySweepInterval != 15*time.Minute:
		t.Errorf("ExpirySweepInterval = %v", o.ExpirySweepInterval)
	}
}
