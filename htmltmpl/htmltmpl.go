// Package htmltmpl is an ergonomic, auto-escaping SSR template layer over the
// standard library's html/template. It owns a parsed template tree loaded from
// an fs.FS (an embed.FS in prod, os.DirFS in dev), composes pages with named
// layouts, and renders them with html/template's context-aware auto-escaping
// fully intact.
//
// The core package is stdlib-only by design. Extra template helpers (including
// the go-sprout helper set) are supplied at the seam via [FuncProvider] so apps
// opt into them explicitly and the framework never inherits a sprig-class SSTI
// surface (env/os/exec funcs) by default.
//
//	r, err := htmltmpl.New(htmltmpl.Options{DefaultLayout: "base"}, logger, clk, embedFS, nil)
//	if err != nil { /* a template failed to parse at startup */ }
//	_ = r.Render(ctx, w, "home", pageData) // wraps "home" in layout "base"
//
// A [*Renderer] is safe for concurrent Render use; each render clones the tree
// so one page's {{block}} override never bleeds into another.
package htmltmpl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/golusoris/golusoris/clock"
)

// maxWalkEntries bounds the fs.FS walk so a hostile or misconfigured tree can
// never make startup unbounded (Power-of-10 rule 2).
const maxWalkEntries = 100_000

// safeURLSchemes is the allowlist for the safeURL helper. Anything else (most
// importantly javascript:, data:, vbscript:) is neutralized to "about:blank".
var safeURLSchemes = map[string]struct{}{
	"http":   {},
	"https":  {},
	"mailto": {},
	"tel":    {},
	"ftp":    {},
}

// FuncProvider is the extension seam. Apps supply extra template funcs (a
// go-sprout registry build, i18n bridges, asset-URL resolvers, ...) by providing
// a FuncProvider to fx; absent one, only the curated safe default funcs apply.
type FuncProvider interface {
	Funcs() template.FuncMap
}

// mapFuncProvider adapts a plain FuncMap to [FuncProvider].
type mapFuncProvider struct{ m template.FuncMap }

func (p mapFuncProvider) Funcs() template.FuncMap { return p.m }

// WithFuncs adapts a plain [template.FuncMap] into a [FuncProvider] suitable for
// fx.Supply. App-supplied funcs override the curated defaults on name clash.
func WithFuncs(m template.FuncMap) FuncProvider { return mapFuncProvider{m: m} }

// Renderer owns the parsed template tree and renders named pages with optional
// layout wrapping. Render* methods are safe for concurrent use.
//
// Each page is parsed into its OWN template set (a clone of the shared
// layouts+partials base plus that one page) so a page's {{define}}/{{block}}
// override never collides with another page on the global block name.
type Renderer struct {
	opts   Options
	logger *slog.Logger
	clk    clock.Clock
	fsys   fs.FS
	funcs  template.FuncMap

	mu    sync.RWMutex // guards sets/base; only contended when hot-reload re-parses
	sets  map[string]*template.Template
	base  *template.Template // shared layouts+partials, for direct partial fragments
	count int                // total parsed template files, for logging
}

// New builds a Renderer, parsing every template matched by opts.Patterns under
// fsys at construction time. fsys is required; the fx constructor falls back to
// os.DirFS(opts.Dir) when no fs.FS is injected. provider may be nil.
func New(
	opts Options,
	logger *slog.Logger,
	clk clock.Clock,
	fsys fs.FS,
	provider FuncProvider,
) (*Renderer, error) {
	if logger == nil {
		return nil, errors.New("htmltmpl: nil logger")
	}
	if clk == nil {
		return nil, errors.New("htmltmpl: nil clock")
	}
	if fsys == nil {
		return nil, errors.New("htmltmpl: nil fs.FS (set htmltmpl.dir or fx.Supply an fs.FS)")
	}
	r := &Renderer{
		opts:   opts.withDefaults(),
		logger: logger,
		clk:    clk,
		fsys:   fsys,
	}
	r.funcs = r.defaultFuncs()
	if provider != nil {
		for name, fn := range provider.Funcs() {
			r.funcs[name] = fn // app funcs win on name clash — documented seam
		}
	}
	p, err := r.parse()
	if err != nil {
		return nil, err
	}
	r.sets = p.sets
	r.base = p.base
	r.count = p.count
	logger.Info(
		"htmltmpl: parsed templates",
		slog.Int("count", p.count),
		slog.Int("pages", len(p.sets)),
		slog.Bool("hot_reload", r.opts.hotReload()),
	)
	return r, nil
}

// parse walks fsys (stdlib Glob has no ** support), builds a shared base of
// layouts+partials, then derives one isolated template set per page so a page's
// {{define}} overrides never collide on the global block name.
func (r *Renderer) parse() (parsed, error) {
	files, err := r.matches()
	if err != nil {
		return parsed{}, err
	}
	var pages []pageFile
	base := r.newBase()
	for _, name := range files {
		b, rErr := fs.ReadFile(r.fsys, name)
		if rErr != nil {
			return parsed{}, fmt.Errorf("htmltmpl: read %q: %w", name, rErr)
		}
		if r.isShared(name) {
			if _, pErr := base.New(name).Parse(string(b)); pErr != nil {
				return parsed{}, fmt.Errorf("htmltmpl: parse layout/partial %q: %w", name, pErr)
			}
			continue
		}
		pages = append(pages, pageFile{name: name, body: string(b)})
	}
	sets, err := r.derivePageSets(base, pages)
	if err != nil {
		return parsed{}, err
	}
	return parsed{sets: sets, base: base, count: len(files)}, nil
}

// parsed is the result of one parse pass: the per-page sets, the shared base
// (for direct partial fragments) and the total file count.
type parsed struct {
	sets  map[string]*template.Template
	base  *template.Template
	count int
}

// pageFile is a parsed-once page name and its raw body.
type pageFile struct {
	name string
	body string
}

// newBase returns an empty base template configured with delimiters, strict
// option and the merged funcmap; page sets are cloned from it.
func (r *Renderer) newBase() *template.Template {
	base := template.New("").Delims(r.opts.Delims[0], r.opts.Delims[1]).Funcs(r.funcs)
	if r.opts.Strict {
		base = base.Option("missingkey=error")
	}
	return base
}

// derivePageSets clones the base per page and parses that page into its clone so
// each page's block overrides are isolated.
func (r *Renderer) derivePageSets(
	base *template.Template,
	pages []pageFile,
) (map[string]*template.Template, error) {
	sets := make(map[string]*template.Template, len(pages))
	for _, pg := range pages {
		clone, cErr := base.Clone()
		if cErr != nil {
			return nil, fmt.Errorf("htmltmpl: clone base for %q: %w", pg.name, cErr)
		}
		if _, pErr := clone.New(pg.name).Parse(pg.body); pErr != nil {
			return nil, fmt.Errorf("htmltmpl: parse page %q: %w", pg.name, pErr)
		}
		sets[pg.name] = clone
	}
	return sets, nil
}

// isShared reports whether name lives under the layouts or partials subdir and
// is therefore parsed into the shared base rather than treated as a page.
func (r *Renderer) isShared(name string) bool {
	for _, sub := range []string{r.opts.Layouts, r.opts.Partials} {
		if sub == "" {
			continue
		}
		if name == sub || strings.HasPrefix(name, sub+"/") {
			return true
		}
	}
	return false
}

// matches returns the sorted set of file paths under fsys matching any pattern.
func (r *Renderer) matches() ([]string, error) {
	seen := make(map[string]struct{})
	out := make([]string, 0, 16)
	count := 0
	walkErr := fs.WalkDir(r.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("htmltmpl: walk %q: %w", p, err)
		}
		count++
		if count > maxWalkEntries {
			return fmt.Errorf("htmltmpl: template tree exceeds %d entries", maxWalkEntries)
		}
		if d.IsDir() {
			return nil
		}
		if !matchAny(r.opts.Patterns, p) {
			return nil
		}
		if _, dup := seen[p]; dup {
			return nil
		}
		seen[p] = struct{}{}
		out = append(out, p)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("htmltmpl: no templates matched %v", r.opts.Patterns)
	}
	return out, nil
}

// matchAny reports whether p matches any glob pattern, supporting a leading or
// embedded ** as "any number of path segments" (stdlib path.Match lacks **).
func matchAny(patterns []string, p string) bool {
	for _, pat := range patterns {
		if matchDoubleStar(pat, p) {
			return true
		}
	}
	return false
}

// matchDoubleStar matches pat against p with ** spanning path segments. It is a
// bounded, allocation-light matcher over the path's basename + suffix patterns.
func matchDoubleStar(pat, p string) bool {
	if !strings.Contains(pat, "**") {
		ok, err := path.Match(pat, p)
		return err == nil && ok
	}
	// Split on the first ** and require prefix/suffix to match around it.
	idx := strings.Index(pat, "**")
	prefix := strings.TrimSuffix(pat[:idx], "/")
	suffix := strings.TrimPrefix(pat[idx+2:], "/")
	if prefix != "" && !strings.HasPrefix(p, prefix) {
		return false
	}
	if suffix == "" {
		return true
	}
	// Suffix may itself be a glob (e.g. "*.gohtml") matched against the basename.
	ok, err := path.Match(suffix, path.Base(p))
	return err == nil && ok
}

// reloadIfNeeded re-parses all sets when hot-reload is on. v1 re-parses on every
// render call under hot-reload (dev-only); prod keeps the cached sets.
func (r *Renderer) reloadIfNeeded() error {
	if !r.opts.hotReload() {
		return nil
	}
	p, err := r.parse()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.sets = p.sets
	r.base = p.base
	r.count = p.count
	r.mu.Unlock()
	return nil
}

// setFor returns the template set to execute name from: the page's own isolated
// set, or — for a shared partial rendered directly as a fragment — a clone of
// the base. The bool reports whether name is resolvable at all.
func (r *Renderer) setFor(name string) (*template.Template, bool, error) {
	r.mu.RLock()
	set, ok := r.sets[name]
	base := r.base
	r.mu.RUnlock()
	if ok {
		return set, true, nil
	}
	if base.Lookup(name) == nil {
		return nil, false, nil
	}
	clone, err := base.Clone()
	if err != nil {
		return nil, false, fmt.Errorf("htmltmpl: clone base for %q: %w", name, err)
	}
	return clone, true, nil
}

// Render executes the named page, wrapping it in Options.DefaultLayout when set.
// Output is fully auto-escaped by html/template.
func (r *Renderer) Render(ctx context.Context, w io.Writer, name string, data any) error {
	return r.RenderLayout(ctx, w, r.opts.DefaultLayout, name, data)
}

// RenderLayout executes page name wrapped in the given layout. An empty layout
// renders the page directly. The layout pulls the page in via {{block}} /
// {{template}}; both resolve within the page's isolated set, so a page's
// {{define}} override never collides with another page's.
func (r *Renderer) RenderLayout(
	ctx context.Context,
	w io.Writer,
	layout, name string,
	data any,
) error {
	if err := r.reloadIfNeeded(); err != nil {
		return err
	}
	set, ok, err := r.setFor(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("htmltmpl: template %q not found", name)
	}
	target := name
	if layout != "" {
		if set.Lookup(layout) == nil {
			return fmt.Errorf("htmltmpl: layout %q not found for page %q", layout, name)
		}
		target = layout
	}
	if err = set.ExecuteTemplate(w, target, data); err != nil {
		return fmt.Errorf("htmltmpl: execute %q: %w", target, err)
	}
	return nil
}

// RenderFragment renders a single page/partial with no layout — the HTMX path,
// mirroring httpx/htmx's partial-response convention.
func (r *Renderer) RenderFragment(ctx context.Context, w io.Writer, name string, data any) error {
	return r.RenderLayout(ctx, w, "", name, data)
}

// RenderHTTP buffers the full render before touching w, so a mid-render template
// error yields a 500 with no bytes written rather than a half-written 200. On
// success it sets Content-Type text/html; charset=utf-8 and the given status.
func (r *Renderer) RenderHTTP(
	w http.ResponseWriter,
	req *http.Request,
	status int,
	name string,
	data any,
) error {
	var buf bytes.Buffer
	if err := r.Render(req.Context(), &buf, name, data); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("htmltmpl: write response: %w", err)
	}
	return nil
}
