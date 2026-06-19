package tus_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/goleak"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/storage"
	"github.com/golusoris/golusoris/storage/tus"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// writeConfig writes a YAML config file into a temp dir and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// enabledConfig builds a YAML config enabling tus over a local bucket at dir.
// The short grace timeout keeps tusd's per-request delayedContext goroutines
// from outliving the test (goleak.VerifyTestMain asserts no leaks).
func enabledConfig(dir string, extra ...string) string {
	var body strings.Builder
	body.WriteString("storage:\n  local:\n    path: " + dir +
		"\n  tus:\n    enabled: true\n    graceful_completion_timeout: 50ms\n")
	for _, line := range extra {
		body.WriteString("    " + line + "\n")
	}
	return body.String()
}

// bootHandler boots storage + tus modules against a YAML body and returns the
// handler, the backing bucket, and a started/stopped fx app.
func bootHandler(t *testing.T, body string) (*tus.Handler, storage.Bucket) {
	t.Helper()
	cfg, err := config.New(config.Options{Files: []string{writeConfig(t, body)}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var (
		h      *tus.Handler
		bucket storage.Bucket
	)
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		storage.Module,
		tus.Module,
		fx.Populate(&h, &bucket),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	if err = app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if stopErr := app.Stop(ctx); stopErr != nil {
			t.Fatalf("Stop: %v", stopErr)
		}
	})
	return h, bucket
}

// mountServer mounts the handler on a fresh chi router behind httptest.
func mountServer(t *testing.T, h *tus.Handler) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	h.Mount(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

const tusVersion = "1.0.0"

// createUpload POSTs a new upload of the given length and returns its URL.
func createUpload(t *testing.T, base string, length int) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base, nil)
	req.Header.Set("Tus-Resumable", tusVersion)
	req.Header.Set("Upload-Length", strconv.Itoa(length))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		t.Fatal("missing Location header")
	}
	return loc
}

// patch sends a PATCH at offset and returns the new server offset.
func patch(t *testing.T, url string, offset int, data string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPatch, url, strings.NewReader(data))
	req.Header.Set("Tus-Resumable", tusVersion)
	req.Header.Set("Upload-Offset", strconv.Itoa(offset))
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PATCH status = %d, want 204", resp.StatusCode)
	}
	got, _ := strconv.Atoi(resp.Header.Get("Upload-Offset"))
	return got
}

// headOffset issues a HEAD and returns the reported Upload-Offset.
func headOffset(t *testing.T, url string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodHead, url, nil)
	req.Header.Set("Tus-Resumable", tusVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200", resp.StatusCode)
	}
	got, _ := strconv.Atoi(resp.Header.Get("Upload-Offset"))
	return got
}

// TestE2E_FullUpload drives create -> patch -> head -> patch (finish) over the
// real tusd handler and asserts the bytes land in the bucket exactly once.
func TestE2E_FullUpload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h, bucket := bootHandler(t, enabledConfig(dir))

	var completes atomic.Int32
	var gotKey atomic.Value
	gotKey.Store("")
	h.OnComplete(func(_ context.Context, c tus.CompletedUpload) error {
		completes.Add(1)
		gotKey.Store(c.Key)
		return nil
	})

	srv := mountServer(t, h)
	base := srv.URL + h.BasePath()

	url := createUpload(t, base, 11)
	if off := patch(t, url, 0, "hello "); off != 6 {
		t.Fatalf("offset after first patch = %d, want 6", off)
	}
	if off := headOffset(t, url); off != 6 {
		t.Fatalf("HEAD offset = %d, want 6", off)
	}
	if off := patch(t, url, 6, "world"); off != 11 {
		t.Fatalf("offset after finish = %d, want 11", off)
	}

	if completes.Load() != 1 {
		t.Fatalf("OnComplete fired %d times, want 1", completes.Load())
	}
	key, _ := gotKey.Load().(string)
	if !strings.HasPrefix(key, "uploads/") {
		t.Fatalf("completion key = %q, want uploads/ prefix", key)
	}
	rc, obj, err := bucket.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("bucket.Get %q: %v", key, err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if string(body) != "hello world" || obj.Size != 11 {
		t.Fatalf("bucket body = %q size = %d", body, obj.Size)
	}
}

// TestE2E_ResumeAfterInterrupt verifies a HEAD-then-resume path mid-upload.
func TestE2E_ResumeAfterInterrupt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h, bucket := bootHandler(t, enabledConfig(dir))
	srv := mountServer(t, h)
	base := srv.URL + h.BasePath()

	url := createUpload(t, base, 9)
	patch(t, url, 0, "abc")

	// Client "reconnects" and asks where it left off, then resumes.
	if off := headOffset(t, url); off != 3 {
		t.Fatalf("resume offset = %d, want 3", off)
	}
	patch(t, url, 3, "def")
	patch(t, url, 6, "ghi")

	objs, err := bucket.List(context.Background(), storage.ListOptions{Prefix: "uploads/"})
	if err != nil || len(objs) != 1 {
		t.Fatalf("bucket list: n=%d err=%v", len(objs), err)
	}
}

// TestModule_ProvidesHandler asserts the fx graph provides a *tus.Handler with
// the configured BasePath and starts/stops the drain goroutine cleanly.
func TestModule_ProvidesHandler(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h, _ := bootHandler(t, enabledConfig(dir, "base_path: /uploads/"))
	if h == nil {
		t.Fatal("expected non-nil *tus.Handler")
	}
	if h.BasePath() != "/uploads/" {
		t.Fatalf("BasePath = %q, want /uploads/", h.BasePath())
	}
}

// TestModule_DefaultBasePath confirms the documented default when unset.
func TestModule_DefaultBasePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h, _ := bootHandler(t, enabledConfig(dir))
	if h.BasePath() != "/files/" {
		t.Fatalf("BasePath = %q, want /files/", h.BasePath())
	}
}

// TestServeHTTP_DirectMount mounts the handler via r.Mount(BasePath, h) and
// drives a create to confirm the ServeHTTP path routes correctly.
func TestServeHTTP_DirectMount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h, _ := bootHandler(t, enabledConfig(dir))

	r := chi.NewRouter()
	r.Mount(h.BasePath(), h)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	url := createUpload(t, srv.URL+h.BasePath(), 3)
	if off := patch(t, url, 0, "xyz"); off != 3 {
		t.Fatalf("offset = %d, want 3", off)
	}
}
