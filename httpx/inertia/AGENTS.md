<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/inertia/

Inertia.js v2 server adapter over `github.com/romsar/gonertia/v3`. Opt-in fx
module — provides `*inertia.Inertia` (re-export of `*gonertia.Inertia`). It
**mounts no routes**: app installs `i.Middleware` on its chi router and
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
 directly for those; this module only owns wiring.
- `RootFS` is **optional** (`fx.In` + `optional:"true"`). Supply app's
 `embed.FS` to read template + manifest from embedded bundle; omit it
 and module reads `root_template` / `manifest_path` from disk.

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
| `inertia.ssr.timeout` | `30s` | Hard bound for one SSR render request |

Multi-word leaf keys with underscores (`root_template`, `manifest_path`,
`container_id`, `encrypt_history`) must be declared in
`config.Options.CompoundKeys` when set via env, otherwise koanf splits underscore (`APP_INERTIA_ROOT_TEMPLATE` -> `inertia.root.template`). YAML/JSON
config files need no such declaration.

## Why gonertia/v3

- **Zero third-party deps** — pure `net/http`, adds nothing to supply-chain
 surface (clean govulncheck/SLSA story). MIT-licensed.
- **Full Inertia.js v2 protocol** — tracks `inertiajs/inertia-laravel`:
 Optional/Defer/Merge/Always/Scroll props, encrypted history, partial reloads,
 version-mismatch 409 handshake, SSR. Ships first-class test assertion
 helpers (`AssertFromBytes` -> `AssertComponent/AssertProps/AssertVersion`).
- **Router-agnostic** — `Middleware` is `func(http.Handler) http.Handler`, drops
 straight onto chi. Constructors accept `fs.FS` (`NewFromFileFS`,
 `WithVersionFromFileFS`), fitting embed--shell deployment model.

Alternatives considered (see ADR): `petaki/inertia-go` (older Inertia v1 shape —
no deferred/merge/always helpers, no assertion helpers); `elipZis/inertia-echo`
(archived, Echo-coupled); hand-rolling protocol (partial reloads + version
dance are exactly where DIY silently diverges from JS client).

## Notes

- **adapter is useless without frontend contract**: built JS bundle,  `root.html` with `{{ .inertia }}` / `{{ .inertiaHead }}` placeholders, and  matching `@inertiajs/{vue,react,svelte}` client. Without them browser
 renders blank page with no server error — app-layer concern, flagged here.
- **Version is md5-hashed**: `WithVersion("v1")` stores `md5("v1")`. asset
 version client receives (and must echo in `X-Inertia-Version`) is hash,
 not raw string. Tests read it from rendered page rather than asserting
 raw value.
- **gonertia injects `errors` prop** (validation-errors bag) into every
 page — exact `AssertProps` comparisons must include it.
- **Logger impedance**: gonertia's `Logger` is `Printf`/`Println`, not slog.  module ships `slogLogger` adapter routed through `WithLogger` so debug output
 flows through framework slog handler (no stdlib `log` / `fmt.Println`).
- **clock rule N/in request path**: gonertia derives asset version by
 file checksum, not time; this module adds no `time.Now` logic.
- No fx lifecycle hook: base adapter owns no goroutine/connection.  in-process SSR manager would register `OnStart`/`OnStop` (never `init()`).
- Decoupled from `httpx/vite`: asset versioning goes through gonertia's
 manifest-checksum option, so two modules stay independent.

See `docs/adr/0009-gonertia-for-inertia-adapter.md`.
