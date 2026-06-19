# Agent guide — httpx/inertia/

Inertia.js v2 server adapter over `github.com/romsar/gonertia/v3`. Opt-in fx
module — provides `*inertia.Inertia` (a re-export of `*gonertia.Inertia`). It
**mounts no routes**: the app installs `i.Middleware` on its chi router and
calls `i.Render` from handlers, mirroring how `storage.Module` only provides
`storage.Bucket`.

## API

```go
fx.New(
    golusoris.Core,
    inertia.Module,                        // provides *inertia.Inertia
    fx.Supply(inertia.RootFS{FS: webFS}),  // OPTIONAL: app's embed.FS
    fx.Invoke(func(i *inertia.Inertia, r chi.Router) {
        r.Use(i.Middleware)                // installs the X-Inertia handshake
        r.Get("/", func(w http.ResponseWriter, req *http.Request) {
            _ = i.Render(w, req, "Dashboard", inertia.Props{"user": u})
        })
    }),
)
```

- `inertia.Props` re-exports `gonertia.Props` so apps don't import gonertia.
- Partial-reload / deferred / merge / always prop helpers live on gonertia
  (`gonertia.Optional`, `.Defer`, `.Merge`, `.Always`, `.Scroll`) — import it
  directly for those; this module only owns the wiring.
- `RootFS` is **optional** (`fx.In` + `optional:"true"`). Supply the app's
  `embed.FS` to read the template + manifest from the embedded bundle; omit it
  and the module reads `root_template` / `manifest_path` from disk.

## Config (env: `APP_INERTIA_*`)

| Key | Default | Notes |
|---|---|---|
| `inertia.root_template` | `web/root.html` | HTML shell with `{{ .inertia }}` + `{{ .inertiaHead }}` |
| `inertia.version` | `""` | Pins the asset version; empty -> derive from manifest |
| `inertia.manifest_path` | `web/dist/.vite/manifest.json` | Vite manifest for checksum-based version |
| `inertia.container_id` | `app` | Root DOM element id |
| `inertia.encrypt_history` | `false` | Inertia global history encryption |
| `inertia.ssr.enabled` | `false` | Server-side rendering via Node sidecar |
| `inertia.ssr.url` | `http://127.0.0.1:13714` | SSR sidecar render endpoint |

Multi-word leaf keys with underscores (`root_template`, `manifest_path`,
`container_id`, `encrypt_history`) must be declared in
`config.Options.CompoundKeys` when set via env, otherwise koanf splits the
underscore (`APP_INERTIA_ROOT_TEMPLATE` -> `inertia.root.template`). YAML/JSON
config files need no such declaration.

## Why gonertia/v3

- **Zero third-party deps** — pure `net/http`, adds nothing to the supply-chain
  surface (clean govulncheck/SLSA story). MIT-licensed.
- **Full Inertia.js v2 protocol** — tracks `inertiajs/inertia-laravel`:
  Optional/Defer/Merge/Always/Scroll props, encrypted history, partial reloads,
  the version-mismatch 409 handshake, SSR. Ships first-class test assertion
  helpers (`AssertFromBytes` -> `AssertComponent/AssertProps/AssertVersion`).
- **Router-agnostic** — `Middleware` is `func(http.Handler) http.Handler`, drops
  straight onto chi. Constructors accept `fs.FS` (`NewFromFileFS`,
  `WithVersionFromFileFS`), fitting the embed-the-shell deployment model.

Alternatives considered (see ADR): `petaki/inertia-go` (older Inertia v1 shape —
no deferred/merge/always helpers, no assertion helpers); `elipZis/inertia-echo`
(archived, Echo-coupled); hand-rolling the protocol (partial reloads + version
dance are exactly where a DIY silently diverges from the JS client).

## Notes

- **The adapter is useless without a frontend contract**: a built JS bundle, a
  `root.html` with `{{ .inertia }}` / `{{ .inertiaHead }}` placeholders, and the
  matching `@inertiajs/{vue,react,svelte}` client. Without them the browser
  renders a blank page with no server error — an app-layer concern, flagged here.
- **Version is md5-hashed**: `WithVersion("v1")` stores `md5("v1")`. The asset
  version the client receives (and must echo in `X-Inertia-Version`) is the hash,
  not the raw string. Tests read it from a rendered page rather than asserting
  the raw value.
- **gonertia injects an `errors` prop** (the validation-errors bag) into every
  page — exact `AssertProps` comparisons must include it.
- **Logger impedance**: gonertia's `Logger` is `Printf`/`Println`, not slog. The
  module ships a `slogLogger` adapter routed through `WithLogger` so debug output
  flows through the framework slog handler (no stdlib `log` / `fmt.Println`).
- **clock rule N/A in the request path**: gonertia derives the asset version by
  file checksum, not time; this module adds no `time.Now` logic.
- No fx lifecycle hook: the base adapter owns no goroutine/connection. An
  in-process SSR manager would register `OnStart`/`OnStop` (never `init()`).
- Decoupled from `httpx/vite`: asset versioning goes through gonertia's
  manifest-checksum option, so the two modules stay independent.

See [ADR-0100](../../docs/adr/0100-gonertia-for-inertia-adapter.md).
