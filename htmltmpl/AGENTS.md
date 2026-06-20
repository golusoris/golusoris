# Agent guide — htmltmpl/

Ergonomic, auto-escaping SSR template layer over stdlib `html/template`. Owns a
parsed tree loaded from an `fs.FS` (an `embed.FS` in prod, `os.DirFS` in dev),
composes pages with named layouts, and renders with context-aware
auto-escaping fully intact.

## API

```go
r, err := htmltmpl.New(Options{DefaultLayout: "base"}, logger, clk, fsys, provider)
r.Render(ctx, w, "home", data)              // wraps "home" in DefaultLayout
r.RenderLayout(ctx, w, "base", "home", data)
r.RenderFragment(ctx, w, "partial", data)   // no layout — the HTMX path
r.RenderHTTP(w, req, status, "home", data)  // buffers; error => clean 500, no half-write

type FuncProvider interface{ Funcs() template.FuncMap }
htmltmpl.WithFuncs(template.FuncMap) FuncProvider // adapt a plain map for fx.Supply
```

A `*Renderer` is safe for concurrent `Render` use; each page lives in its own
cloned set so one page's `{{block}}` override never bleeds into another.

## Wiring

```go
fx.New(
    golusoris.Core,          // *config.Config, *slog.Logger, clock.Clock
    htmltmpl.Module,         // provides *htmltmpl.Renderer
    fx.Supply(fs.FS(embedFS)),               // optional: omit => os.DirFS(htmltmpl.dir)
    fx.Provide(myFuncProvider),              // optional: extra template funcs
)
```

Requires `*config.Config`, `*slog.Logger`, `clock.Clock`. `fs.FS` and
`FuncProvider` are optional fx inputs. Config keys live under the `htmltmpl`
prefix (`dir`, `patterns`, `layouts`, `partials`, `default_layout`, `delims`,
`hot_reload`, `strict`).

## Notes

- **Stdlib-only core by design.** The curated default funcs deliberately omit
  env/os/exec/network helpers (the sprig-class SSTI surface). A go-sprout helper
  set is opt-in via `FuncProvider`; app funcs win on name clash.
- `hot_reload` is tri-state: explicit wins; nil derives from `APP_ENV`
  (`dev`/`development`/`local` => true, else false). Hot-reload re-parses on
  every render — dev only.
- `**` in patterns spans path segments (stdlib `path.Match` lacks it); the walk
  is bounded to 100k entries (Power-of-10 rule 2).
- `safeURL` allowlists http/https/mailto/tel/ftp and neutralizes everything else
  (javascript:/data:/vbscript:) to `about:blank`.
