# ADR-0009: gonertia/v3 for the Inertia.js server adapter

- **Status**: Accepted
- **Date**: 2026-06-19
- **Deciders**: @lusoris
- **Tags**: http, frontend, inertia

## Context

golusoris standardises on chi (`net/http` handlers) and offers apps a
server-driven SPA path via [Inertia.js](https://inertiajs.com). Inertia's wire
protocol is small but non-trivial: the `X-Inertia` request/response handshake,
the asset-version 409 reload dance, partial reloads (`X-Inertia-Partial-Data`),
deferred/merge/always/scroll props, encrypted history, and an optional Node SSR
round-trip. Getting any of these subtly wrong silently diverges from the JS
client. The framework needs an adapter that ships the full Inertia.js v2
protocol, couples to nothing beyond `net/http`, and keeps the supply-chain
surface clean ([principles.md §2.5](../principles.md): SLSA L3, govulncheck-zero).

## Decision

We will use `github.com/romsar/gonertia/v3` as the Inertia adapter, wired as the
opt-in `httpx/inertia` fx module that provides `*gonertia.Inertia`. The module
mounts no routes — apps install `i.Middleware` on their chi router and call
`i.Render`. A small `slogLogger` adapter bridges gonertia's `Printf`/`Println`
`Logger` to the framework's `*slog.Logger` so debug output never falls back to
the stdlib `log` package.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| `romsar/gonertia/v3` | Zero third-party deps, MIT, pure `net/http`, full Inertia v2 protocol, `fs.FS` constructors, built-in assertion helpers | Recent v3 (2026-03), small maintainer base | Chosen — best protocol coverage with the cleanest dep graph for a server framework. |
| `petaki/inertia-go` | Mature, the lib gonertia derives from | Older Inertia v1 shape: no deferred/merge/always helpers, weaker SSR, no test assertion helpers | Exposes only a subset of the protocol the framework wants. |
| `elipZis/inertia-echo` | — | Archived (2023), hard-coupled to Echo | Dead upstream + wrong router. |
| Hand-roll the protocol | No dependency | Partial reloads, deferred/merge props and the version handshake are exactly where a DIY diverges from the JS client | Vendoring a maintained, zero-dep, MIT lib buys correctness for free. |

## Consequences

- **Positive**: Full Inertia.js v2 protocol with no added third-party deps (clean
  govulncheck/SLSA story). Router-agnostic middleware drops onto chi. `fs.FS`
  constructors fit the embed-the-shell deployment model. Upstream's assertion
  helpers (`AssertFromBytes` -> `AssertComponent/AssertProps/AssertVersion`) give
  the module high-fidelity tests against the real wire format.
- **Negative**: Adds a frontend contract apps must satisfy (built JS bundle +
  `root.html` placeholders + `@inertiajs/*` client) or the browser shows a blank
  page; documented in `httpx/inertia/AGENTS.md`. v3 is young with a small
  maintainer base — mitigated by zero-dep + MIT (forkable) and a small protocol.
- **Neutral / follow-ups**: The `slogLogger` adapter is required (gonertia's
  `Logger` is `Printf`/`Println`, not slog). Asset versioning stays
  checksum-based to avoid any `time.Now` in the request path
  ([clock rule](../../clock/AGENTS.md)). `httpx/inertia` stays decoupled from
  `httpx/vite` — version derivation goes through gonertia's manifest-checksum
  option, not a `vite.Manifest` dependency. An in-process SSR manager, if added,
  registers `OnStart`/`OnStop` fx hooks, never `init()`.

## References

- [Inertia.js protocol](https://inertiajs.com/the-protocol) — wire format.
- [`github.com/romsar/gonertia`](https://github.com/romsar/gonertia) — chosen adapter (v3.0.0).
- [principles.md §2.5](../principles.md) — supply-chain standards (SLSA, govulncheck).
- `httpx/inertia/AGENTS.md` — module guide + frontend contract.
