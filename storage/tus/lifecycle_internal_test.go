package tus

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
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
	agePastMtime(t, scratch.root, "stale", 48*time.Hour)

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
	h.waitDrain(ctx)
}

// TestFireComplete_StopsOnError ensures the callback chain halts on first error.
func TestFireComplete_StopsOnError(t *testing.T) {
	t.Parallel()
	h := &Handler{log: slog.New(slog.DiscardHandler)}
	var ran int
	h.OnComplete(func(context.Context, CompletedUpload) error {
		ran++
		return errBoom
	})
	h.OnComplete(func(context.Context, CompletedUpload) error {
		ran++
		return nil
	})
	if err := h.fireComplete(context.Background(), CompletedUpload{}); err == nil {
		t.Fatal("expected error from first callback")
	}
	if ran != 1 {
		t.Fatalf("ran %d callbacks, want 1 (chain should stop)", ran)
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
	}
}
