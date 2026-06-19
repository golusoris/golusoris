package inertia_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	gonertia "github.com/romsar/gonertia/v3"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/inertia"
)

// rootTemplate is a minimal Inertia root shell with the required placeholders.
const rootTemplate = `<!DOCTYPE html><html><head>{{ .inertiaHead }}</head>` +
	`<body>{{ .inertia }}</body></html>`

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func mapFS() fstest.MapFS {
	return fstest.MapFS{
		"web/root.html": &fstest.MapFile{Data: []byte(rootTemplate)},
		"web/dist/.vite/manifest.json": &fstest.MapFile{
			Data: []byte(`{"main.js":{"file":"assets/main.abc123.js"}}`),
		},
	}
}

func newFromMapFS(t *testing.T, opts inertia.Options) *inertia.Inertia {
	t.Helper()
	i, err := inertia.NewForTest(opts, discardLogger(), inertia.RootFS{FS: mapFS()})
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}
	return i
}

// assetVersion renders a JSON page directly (bypassing the middleware version
// handshake) and extracts the version. gonertia md5-hashes the configured
// version string, so tests must use the hashed value the client would receive
// rather than the raw Options.Version.
func assetVersion(t *testing.T, i *inertia.Inertia) string {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Inertia", "true")
	if err := i.Render(rr, req, "Probe"); err != nil {
		t.Fatalf("Render probe: %v", err)
	}
	var page struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(bytes.NewReader(rr.Body.Bytes())).Decode(&page); err != nil {
		t.Fatalf("decode probe page: %v", err)
	}
	return page.Version
}

func TestLoadOptions_Defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{Watch: false})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts, err := inertia.LoadOptionsForTest(cfg)
	if err != nil {
		t.Fatalf("LoadOptionsForTest: %v", err)
	}

	want := inertia.Options{
		RootTemplate: "web/root.html",
		ManifestPath: "web/dist/.vite/manifest.json",
		ContainerID:  "app",
		SSR:          inertia.SSROptions{URL: "http://127.0.0.1:13714"},
	}
	if opts != want {
		t.Fatalf("defaults = %+v, want %+v", opts, want)
	}
}

func TestLoadOptions_Override(t *testing.T) {
	// No t.Parallel — t.Setenv is incompatible with parallel tests.
	t.Setenv("APP_INERTIA_ROOT_TEMPLATE", "app/shell.html")
	t.Setenv("APP_INERTIA_VERSION", "v9")
	t.Setenv("APP_INERTIA_MANIFEST_PATH", "dist/manifest.json")
	t.Setenv("APP_INERTIA_CONTAINER_ID", "root")
	t.Setenv("APP_INERTIA_ENCRYPT_HISTORY", "true")
	t.Setenv("APP_INERTIA_SSR_ENABLED", "true")
	t.Setenv("APP_INERTIA_SSR_URL", "http://ssr:9999")

	cfg, err := config.New(config.Options{
		EnvPrefix: "APP_",
		Watch:     false,
		CompoundKeys: []string{
			"inertia.root_template",
			"inertia.manifest_path",
			"inertia.container_id",
			"inertia.encrypt_history",
		},
	})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts, err := inertia.LoadOptionsForTest(cfg)
	if err != nil {
		t.Fatalf("LoadOptionsForTest: %v", err)
	}

	want := inertia.Options{
		RootTemplate:   "app/shell.html",
		Version:        "v9",
		ManifestPath:   "dist/manifest.json",
		ContainerID:    "root",
		EncryptHistory: true,
		SSR:            inertia.SSROptions{Enabled: true, URL: "http://ssr:9999"},
	}
	if opts != want {
		t.Fatalf("override = %+v, want %+v", opts, want)
	}
}

func TestNewInertia_FromMapFS(t *testing.T) {
	t.Parallel()
	i := newFromMapFS(t, inertia.Options{
		RootTemplate: "web/root.html",
		ManifestPath: "web/dist/.vite/manifest.json",
		ContainerID:  "app",
	})
	if i == nil {
		t.Fatal("nil inertia")
	}
}

func TestNewInertia_FromDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "root.html")
	if err := os.WriteFile(path, []byte(rootTemplate), 0o600); err != nil {
		t.Fatalf("write template: %v", err)
	}
	i, err := inertia.NewForTest(
		inertia.Options{RootTemplate: path, Version: "v1", ContainerID: "app"},
		discardLogger(),
		inertia.RootFS{},
	)
	if err != nil {
		t.Fatalf("NewForTest disk: %v", err)
	}
	if i == nil {
		t.Fatal("nil inertia")
	}
}

// TestNewInertia_AllOptions exercises the SSR, encrypt-history and on-disk
// manifest-checksum branches of buildOptions.
func TestNewInertia_AllOptions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "root.html")
	manifest := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(tmpl, []byte(rootTemplate), 0o600); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if err := os.WriteFile(manifest, []byte(`{"main.js":{"file":"main.js"}}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	i, err := inertia.NewForTest(
		inertia.Options{
			RootTemplate:   tmpl,
			ManifestPath:   manifest, // Version empty -> derive from disk manifest
			ContainerID:    "app",
			EncryptHistory: true,
			SSR:            inertia.SSROptions{Enabled: true, URL: "http://127.0.0.1:13714"},
		},
		discardLogger(),
		inertia.RootFS{},
	)
	if err != nil {
		t.Fatalf("NewForTest all-options: %v", err)
	}
	if i == nil {
		t.Fatal("nil inertia")
	}
}

func TestNewInertia_MissingTemplate(t *testing.T) {
	t.Parallel()
	_, err := inertia.NewForTest(
		inertia.Options{RootTemplate: "does/not/exist.html", ContainerID: "app"},
		discardLogger(),
		inertia.RootFS{},
	)
	if err == nil {
		t.Fatal("expected error for missing template")
	}
	if !strings.Contains(err.Error(), "httpx/inertia:") {
		t.Fatalf("error not wrapped with package prefix: %v", err)
	}
}

// TestRender_XHR verifies an Inertia XHR yields the JSON page object with the
// right component, props and version.
func TestRender_XHR(t *testing.T) {
	t.Parallel()
	i := newFromMapFS(t, inertia.Options{
		RootTemplate: "web/root.html",
		Version:      "v1",
		ContainerID:  "app",
	})

	version := assetVersion(t, i)
	h := i.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := i.Render(w, r, "Dashboard", inertia.Props{"greeting": "hi"}); err != nil {
			t.Errorf("Render: %v", err)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Inertia", "true")
	req.Header.Set("X-Inertia-Version", version)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("X-Inertia"); got != "true" {
		t.Fatalf("X-Inertia response header = %q, want true", got)
	}

	a := gonertia.AssertFromBytes(t, rr.Body.Bytes())
	a.AssertComponent("Dashboard")
	a.AssertVersion(version)
	// gonertia always injects an "errors" prop (the validation-errors bag).
	a.AssertProps(inertia.Props{"greeting": "hi", "errors": map[string]any{}})
}

// TestRender_HTML verifies a non-Inertia request gets the HTML shell with the
// {{ .inertia }} placeholder populated by the data-page div.
func TestRender_HTML(t *testing.T) {
	t.Parallel()
	i := newFromMapFS(t, inertia.Options{
		RootTemplate: "web/root.html",
		Version:      "v1",
		ContainerID:  "app",
	})

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := i.Render(w, r, "Home", inertia.Props{"x": 1}); err != nil {
			t.Errorf("Render: %v", err)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Fatalf("response is not the HTML shell: %s", body)
	}
	if !strings.Contains(body, `id="app"`) {
		t.Fatalf("HTML shell missing inertia container: %s", body)
	}
	if !strings.Contains(body, "Home") {
		t.Fatalf("HTML shell missing component name: %s", body)
	}
}

// TestVersionHandshake verifies a stale asset version triggers the 409 +
// X-Inertia-Location reload handshake.
func TestVersionHandshake(t *testing.T) {
	t.Parallel()
	i := newFromMapFS(t, inertia.Options{
		RootTemplate: "web/root.html",
		Version:      "v2",
		ContainerID:  "app",
	})

	h := i.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := i.Render(w, r, "Dashboard"); err != nil {
			t.Errorf("Render: %v", err)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Inertia", "true")
	req.Header.Set("X-Inertia-Version", "stale")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
	if got := rr.Header().Get("X-Inertia-Location"); got == "" {
		t.Fatal("missing X-Inertia-Location on version mismatch")
	}
}

// TestPartialReload verifies X-Inertia-Partial-Data filters props to only the
// requested keys.
func TestPartialReload(t *testing.T) {
	t.Parallel()
	i := newFromMapFS(t, inertia.Options{
		RootTemplate: "web/root.html",
		Version:      "v1",
		ContainerID:  "app",
	})

	version := assetVersion(t, i)
	h := i.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		props := inertia.Props{"keep": "yes", "drop": "no"}
		if err := i.Render(w, r, "Dashboard", props); err != nil {
			t.Errorf("Render: %v", err)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Inertia", "true")
	req.Header.Set("X-Inertia-Version", version)
	req.Header.Set("X-Inertia-Partial-Data", "keep")
	req.Header.Set("X-Inertia-Partial-Component", "Dashboard")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	props := decodeProps(t, rr.Body.Bytes())
	if _, ok := props["keep"]; !ok {
		t.Fatalf("partial reload dropped requested prop: %v", props)
	}
	if _, ok := props["drop"]; ok {
		t.Fatalf("partial reload kept unrequested prop: %v", props)
	}
}

// TestSharedProps verifies shared props injected via context survive the
// middleware chain into the rendered page.
func TestSharedProps(t *testing.T) {
	t.Parallel()
	i := newFromMapFS(t, inertia.Options{
		RootTemplate: "web/root.html",
		Version:      "v1",
		ContainerID:  "app",
	})

	version := assetVersion(t, i)
	share := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := gonertia.SetProp(r.Context(), "authed", true)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	h := i.Middleware(share(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := i.Render(w, r, "Dashboard", inertia.Props{"page": 1}); err != nil {
			t.Errorf("Render: %v", err)
		}
	})))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Inertia", "true")
	req.Header.Set("X-Inertia-Version", version)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// i.Props is JSON-decoded, so numeric props come back as float64.
	a := gonertia.AssertFromBytes(t, rr.Body.Bytes())
	a.AssertProps(inertia.Props{"page": float64(1), "authed": true, "errors": map[string]any{}})
}

// decodeProps extracts the "props" object from an Inertia JSON page response.
func decodeProps(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var page struct {
		Props map[string]any `json:"props"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	return page.Props
}

// TestSlogLogger verifies the gonertia Logger adapter routes Printf/Println
// through the injected slog handler (no fmt.Println leakage).
func TestSlogLogger(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	l := inertia.NewSlogLoggerForTest(logger)
	l.Printf("hello %s", "world")
	l.Println("second", "line")

	out := buf.String()
	if !strings.Contains(out, "hello world") {
		t.Fatalf("Printf not forwarded to slog: %q", out)
	}
	if !strings.Contains(out, "second line") {
		t.Fatalf("Println not forwarded to slog: %q", out)
	}
}

func TestModule_NotNil(t *testing.T) {
	t.Parallel()
	if inertia.Module == nil {
		t.Fatal("Module is nil")
	}
}
