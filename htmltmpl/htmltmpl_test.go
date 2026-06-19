package htmltmpl_test

import (
	"bytes"
	"context"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/htmltmpl"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
}

func mustNew(t *testing.T, files fstest.MapFS, opts htmltmpl.Options, p htmltmpl.FuncProvider) *htmltmpl.Renderer {
	t.Helper()
	r, err := htmltmpl.New(opts, testLogger(), clock.NewFake(), files, p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func render(t *testing.T, r *htmltmpl.Renderer, name string, data any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.Render(context.Background(), &buf, name, data); err != nil {
		t.Fatalf("Render(%q): %v", name, err)
	}
	return buf.String()
}

// TestAutoEscaping feeds hostile data into each html/template context and asserts
// the auto-escaper neutralized it. This is the security-critical core.
func TestAutoEscaping(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"body.gohtml":  {Data: []byte(`<p>{{.}}</p>`)},
		"attr.gohtml":  {Data: []byte(`<a title="{{.}}">x</a>`)},
		"href.gohtml":  {Data: []byte(`<a href="{{.}}">x</a>`)},
		"jsctx.gohtml": {Data: []byte(`<script>var x = {{.}};</script>`)},
	}
	r := mustNew(t, files, htmltmpl.Options{}, nil)

	tests := []struct {
		name           string
		tmpl           string
		data           any
		mustNotContain []string
		mustContain    []string
	}{
		{
			name:           "html body script tag escaped",
			tmpl:           "body.gohtml",
			data:           `<script>alert(1)</script>`,
			mustNotContain: []string{"<script>alert(1)</script>"},
			mustContain:    []string{"&lt;script&gt;"},
		},
		{
			name:           "attribute breakout escaped",
			tmpl:           "attr.gohtml",
			data:           `"><img src=x onerror=alert(1)>`,
			mustNotContain: []string{`"><img`, "onerror=alert(1)>"},
		},
		{
			name:           "javascript url neutralized in href",
			tmpl:           "href.gohtml",
			data:           "javascript:alert(1)",
			mustNotContain: []string{"javascript:alert(1)"},
			mustContain:    []string{"#ZgotmplZ"},
		},
		{
			name:           "js context string injection escaped",
			tmpl:           "jsctx.gohtml",
			data:           `</script><script>alert(1)</script>`,
			mustNotContain: []string{"</script><script>alert(1)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := render(t, r, tt.tmpl, tt.data)
			for _, bad := range tt.mustNotContain {
				if strings.Contains(out, bad) {
					t.Fatalf("output leaked %q:\n%s", bad, out)
				}
			}
			for _, want := range tt.mustContain {
				if !strings.Contains(out, want) {
					t.Fatalf("output missing %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestLayoutComposition asserts a page overriding a {{block}} renders correctly
// and that the override does not bleed into another page rendered after it.
func TestLayoutComposition(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"layouts/base": {Data: []byte(`<html><body>{{block "content" .}}default{{end}}</body></html>`)},
		"home":         {Data: []byte(`{{define "content"}}HOME{{end}}{{template "layouts/base" .}}`)},
		"about":        {Data: []byte(`{{define "content"}}ABOUT{{end}}{{template "layouts/base" .}}`)},
		"plain":        {Data: []byte(`{{template "layouts/base" .}}`)},
	}
	r := mustNew(t, files, htmltmpl.Options{Patterns: []string{"**/*", "*"}}, nil)

	home := render(t, r, "home", nil)
	if !strings.Contains(home, "HOME") {
		t.Fatalf("home missing HOME: %s", home)
	}
	about := render(t, r, "about", nil)
	if !strings.Contains(about, "ABOUT") {
		t.Fatalf("about missing ABOUT: %s", about)
	}
	if strings.Contains(about, "HOME") {
		t.Fatalf("home's block bled into about: %s", about)
	}
	// A page that does not redefine content gets the default block — proving the
	// clone-per-render isolation reset the override.
	plain := render(t, r, "plain", nil)
	if !strings.Contains(plain, "default") {
		t.Fatalf("plain should render default block, got: %s", plain)
	}
}

// TestRenderLayoutExplicit and missing-name error paths.
func TestRenderLayoutErrors(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"page":        {Data: []byte(`hi`)},
		"layouts/lay": {Data: []byte(`<x>{{template "page" .}}</x>`)},
	}
	r := mustNew(t, files, htmltmpl.Options{Patterns: []string{"**/*", "*"}}, nil)
	ctx := context.Background()

	if err := r.RenderLayout(ctx, &bytes.Buffer{}, "missing-layout", "page", nil); err == nil {
		t.Fatal("expected error for missing layout")
	}
	if err := r.RenderLayout(ctx, &bytes.Buffer{}, "", "missing-page", nil); err == nil {
		t.Fatal("expected error for missing page")
	}
	var buf bytes.Buffer
	if err := r.RenderLayout(ctx, &buf, "layouts/lay", "page", nil); err != nil {
		t.Fatalf("RenderLayout: %v", err)
	}
	if !strings.Contains(buf.String(), "<x>hi</x>") {
		t.Fatalf("unexpected output: %s", buf.String())
	}
}

// TestFuncProviderSeam asserts injected funcs are available and that no
// env/os-style funcs leak into the default funcmap.
func TestFuncProviderSeam(t *testing.T) {
	t.Parallel()
	pat := []string{"*"}

	// Default funcs only: upper works.
	rDefault := mustNew(t, fstest.MapFS{"def": {Data: []byte(`{{ "a" | upper }}`)}},
		htmltmpl.Options{Patterns: pat}, nil)
	if got := render(t, rDefault, "def", nil); got != "A" {
		t.Fatalf("upper helper: got %q", got)
	}

	// env must NOT exist by default — parsing a template using it must fail.
	envFiles := fstest.MapFS{"envtry": {Data: []byte(`{{ env "PATH" }}`)}}
	if _, err := htmltmpl.New(htmltmpl.Options{Patterns: pat},
		testLogger(), clock.NewFake(), envFiles, nil); err == nil {
		t.Fatal("expected parse failure: env func must be absent from defaults")
	}

	// With provider: custom func available.
	provider := htmltmpl.WithFuncs(template.FuncMap{
		"shout": func(s string) string { return strings.ToUpper(s) + "!" },
	})
	rCustom := mustNew(t, fstest.MapFS{"custom": {Data: []byte(`{{ shout "hi" }}`)}},
		htmltmpl.Options{Patterns: pat}, provider)
	if got := render(t, rCustom, "custom", nil); got != "HI!" {
		t.Fatalf("custom shout helper: got %q", got)
	}
}

func TestRenderFragment(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"frag": {Data: []byte(`<li>{{.}}</li>`)},
	}
	r := mustNew(t, files, htmltmpl.Options{Patterns: []string{"*"}, DefaultLayout: "missing"}, nil)
	var buf bytes.Buffer
	if err := r.RenderFragment(context.Background(), &buf, "frag", "x"); err != nil {
		t.Fatalf("RenderFragment: %v", err)
	}
	if buf.String() != "<li>x</li>" {
		t.Fatalf("fragment output: %q", buf.String())
	}
}

// TestRenderHTTP asserts full buffering: a mid-render error yields a 500 with no
// page bytes, and success sets the content type + status.
func TestRenderHTTP(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"ok":  {Data: []byte(`<h1>{{.Title}}</h1>`)},
		"bad": {Data: []byte(`{{.Missing.Field}}`)},
	}
	r := mustNew(t, files, htmltmpl.Options{Patterns: []string{"*"}, Strict: true}, nil)

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		err := r.RenderHTTP(rec, req, http.StatusCreated, "ok", map[string]any{"Title": "Hi"})
		if err != nil {
			t.Fatalf("RenderHTTP: %v", err)
		}
		if rec.Code != http.StatusCreated {
			t.Fatalf("status: got %d", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Fatalf("content-type: got %q", ct)
		}
		if !strings.Contains(rec.Body.String(), "<h1>Hi</h1>") {
			t.Fatalf("body: %s", rec.Body.String())
		}
	})

	t.Run("mid-render error yields 500 with no partial page", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		err := r.RenderHTTP(rec, req, http.StatusOK, "bad", map[string]any{})
		if err == nil {
			t.Fatal("expected render error")
		}
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status: got %d, want 500", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<") {
			t.Fatalf("partial page leaked into 500 body: %s", rec.Body.String())
		}
	})
}

// TestHotReload asserts re-parse on render under HotReload=true and a cached tree
// under HotReload=false, using a mutable in-memory FS.
func TestHotReload(t *testing.T) {
	t.Parallel()
	on := true
	off := false

	t.Run("on re-parses", func(t *testing.T) {
		t.Parallel()
		files := fstest.MapFS{"p.gohtml": {Data: []byte(`v1`)}}
		r := mustNew(t, files, htmltmpl.Options{HotReload: &on}, nil)
		if got := render(t, r, "p.gohtml", nil); got != "v1" {
			t.Fatalf("first render: %q", got)
		}
		files["p.gohtml"] = &fstest.MapFile{Data: []byte(`v2`)}
		if got := render(t, r, "p.gohtml", nil); got != "v2" {
			t.Fatalf("hot-reload render: got %q, want v2", got)
		}
	})

	t.Run("off serves cached", func(t *testing.T) {
		t.Parallel()
		files := fstest.MapFS{"p.gohtml": {Data: []byte(`v1`)}}
		r := mustNew(t, files, htmltmpl.Options{HotReload: &off}, nil)
		files["p.gohtml"] = &fstest.MapFile{Data: []byte(`v2`)}
		if got := render(t, r, "p.gohtml", nil); got != "v1" {
			t.Fatalf("cached render: got %q, want v1 (no reload)", got)
		}
	})
}

// TestNowHelperDeterministic asserts the now helper reads the injected clock.
func TestNowHelperDeterministic(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{"t.gohtml": {Data: []byte(`{{ now.Year }}`)}}
	fake := clock.NewFake()
	r, err := htmltmpl.New(htmltmpl.Options{}, testLogger(), fake, files, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := fake.Now().Year()
	var buf bytes.Buffer
	if err = r.Render(context.Background(), &buf, "t.gohtml", nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := buf.String(); got != strconv.Itoa(want) {
		t.Fatalf("now.Year = %q, want %d", got, want)
	}
}

func TestNewValidation(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{"p": {Data: []byte("x")}}
	tests := []struct {
		name   string
		logger *slog.Logger
		clk    clock.Clock
		fsys   fs.FS
	}{
		{"nil logger", nil, clock.NewFake(), files},
		{"nil clock", testLogger(), nil, files},
		{"nil fs", testLogger(), clock.NewFake(), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := htmltmpl.New(htmltmpl.Options{}, tt.logger, tt.clk, tt.fsys, nil); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestNoTemplatesMatched(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{"readme.txt": {Data: []byte("x")}}
	if _, err := htmltmpl.New(htmltmpl.Options{}, testLogger(), clock.NewFake(), files, nil); err == nil {
		t.Fatal("expected error when no templates match patterns")
	}
}

// TestDoubleStarGlob asserts ** spans nested directories.
func TestDoubleStarGlob(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"a.gohtml":            {Data: []byte(`A`)},
		"sub/b.gohtml":        {Data: []byte(`B`)},
		"sub/deep/c.gohtml":   {Data: []byte(`C`)},
		"sub/deep/skip.other": {Data: []byte(`X`)},
	}
	r := mustNew(t, files, htmltmpl.Options{}, nil)
	for _, name := range []string{"a.gohtml", "sub/b.gohtml", "sub/deep/c.gohtml"} {
		if got := render(t, r, name, nil); got == "" {
			t.Fatalf("expected %q to be parsed", name)
		}
	}
}

// TestCustomDelims asserts the Delims override.
func TestCustomDelims(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{"p": {Data: []byte(`[[ "x" | upper ]]`)}}
	r := mustNew(t, files, htmltmpl.Options{Patterns: []string{"*"}, Delims: [2]string{"[[", "]]"}}, nil)
	if got := render(t, r, "p", nil); got != "X" {
		t.Fatalf("custom delims: %q", got)
	}
}

// TestFxWiring is the wiring smoke test: Core-like deps + Module resolve a
// *Renderer with a supplied fs.FS.
func TestFxWiring(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{"p.gohtml": {Data: []byte(`ok`)}}

	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	var resolved *htmltmpl.Renderer
	app := fxtest.New(
		t,
		fx.Supply(cfg),
		fx.Supply(testLogger()),
		fx.Provide(func() clock.Clock { return clock.NewFake() }),
		fx.Supply(fx.Annotate(fs.FS(files), fx.As(new(fs.FS)))),
		htmltmpl.Module,
		fx.Populate(&resolved),
	)
	app.RequireStart()
	defer app.RequireStop()
	if resolved == nil {
		t.Fatal("renderer not resolved")
	}
	out := render(t, resolved, "p.gohtml", nil)
	if out != "ok" {
		t.Fatalf("rendered: %q", out)
	}
}
