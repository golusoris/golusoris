<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0025: Keep hand-written session engine

- **Status**: Accepted
- **Date**: 2026-10-29
- **Deciders**: @lusoris
- **Tags**: `auth`, `session`, `dependencies`

## Context

The package `auth/session` provides server-side HTTP session management keyed by opaque tokens stored in cookies ([`auth/session/session.go`](https://github.com/golusoris/golusoris/blob/main/auth/session/session.go)). It comprises 336 lines of Go code with standard-library-only dependencies outside the framework's core abstractions, verified by race-clean test suites in [`auth/session/*_test.go`](https://github.com/golusoris/golusoris/tree/main/auth/session) (`go test -race ./auth/session/...` exit 0).

Issue #1183 evaluated replacing or backing this implementation with [`github.com/alexedwards/scs/v2`](https://github.com/alexedwards/scs), a widely used Go session package. Inspection of upstream release v2.9.0 (Go proxy tag time 2025-04-17, GitHub release published 2025-07-08, MIT licence) against golusoris architectural invariants and hard rules identified several key constraints:

- **Dependency footprint**: Adding `scs/v2` introduces +1 module to the repository `go.mod` dependency graph (increasing unique module count from 603 to 604).
- **Time determinism and injected clocks**: Upstream hardcodes direct calls to `time.Now()` across its core session lifecycle ([`data.go:42`](https://github.com/alexedwards/scs/blob/v2.9.0/data.go#L42), [`data.go:113`](https://github.com/alexedwards/scs/blob/v2.9.0/data.go#L113), [`data.go:144`](https://github.com/alexedwards/scs/blob/v2.9.0/data.go#L144), [`data.go:301`](https://github.com/alexedwards/scs/blob/v2.9.0/data.go#L301)). This conflicts with `AGENTS.md` hard rule 6 (`Time -> golusoris/core/clock; time.Now() forbidden elsewhere`) and prevents deterministic clockwork-driven expiry verification, such as the fake-clock boundary tests in [`auth/session/session_test.go:370-388`](https://github.com/golusoris/golusoris/blob/main/auth/session/session_test.go#L370-L388).
- **Cookie security defaults**: Upstream defaults `Cookie.Secure` to `false` ([`session.go:120`](https://github.com/alexedwards/scs/blob/v2.9.0/session.go#L120)). In contrast, the golusoris engine mandates secure cookies by default ([`auth/session/session.go:77-79, 176, 182`](https://github.com/golusoris/golusoris/blob/main/auth/session/session.go#L77-L79)), requiring callers to explicitly set `AllowInsecureCookie: true` for isolated local development.
- **Logging conventions**: The default `ErrorFunc` in `scs` logs via standard library `log.Output` ([`session.go:220-221`](https://github.com/alexedwards/scs/blob/v2.9.0/session.go#L220-L221)). While overridable via `SessionManager.ErrorFunc`, this default conflicts with `AGENTS.md` hard rule 7 (`Logs -> golusoris/core/log slog handler; no fmt.Println, no global loggers`).
- **Token renewal race window**: In `scs`, `RenewToken` ([`data.go:282`](https://github.com/alexedwards/scs/blob/v2.9.0/data.go#L282)) deletes the old token from the store before the new token is committed. If a network partition, handler panic, or store failure occurs before the commit succeeds, neither token is valid, resulting in permanent session loss. The golusoris engine ([`auth/session/session.go:150-172`](https://github.com/golusoris/golusoris/blob/main/auth/session/session.go#L150-L172)) commits the new session ID and updates the cookie before deleting the old ID, ensuring that a failure in between leaves a valid session.
- **Control flow and runtime panics**: Upstream raises runtime panics when accessing session values outside active middleware context ([`data.go:613`](https://github.com/alexedwards/scs/blob/v2.9.0/data.go#L613) with `"scs: no session data in context"` and [`data.go:696`](https://github.com/alexedwards/scs/blob/v2.9.0/data.go#L696)). golusoris methods return explicit errors or handle missing sessions without panics.

## Considered options

| Option | Licence | Latest Tag / Date | Maintenance Status | Transitive Deps | Evaluation |
| :--- | :--- | :--- | :--- | :--- | :--- |
| Option a: Replace with `github.com/alexedwards/scs/v2` | MIT | v2.9.0 (2025-07-08) | Not assessed | +1 module (603 -> 604) | Rejects: Breaks public ABI under HISS-14; hardcoded `time.Now()` breaks deterministic clock injection (hard rule 6); default `log.Output` conflicts with slog policy (hard rule 7); `Secure: false` default; panics on missing context. |
| Option b: Adapter between scs Store and existing engine | MIT | v2.9.0 (2025-07-08) | Not assessed | +1 module (603 -> 604) | Rejects: Adds dependency without reducing code volume; adapting `scs.SessionManager` or store abstractions requires adapter shims comparable in size to the existing 336 LOC engine, while clock and renewal semantics remain awkward. |
| Option c: Keep hand-written engine (`auth/session`) | EUPL-1.2 | In-tree | Active (maintainer) | 0 external modules | Chosen: 336 LOC, stdlib-only, race-green; injected clock for expiry, no logging of its own, secure-by-default cookies, save-before-delete token renewal, and no public API change under HISS-14. |

### Option a: Replace with scs directly

Adopting `scs` directly would replace the current synchronous `Manager` API (`Load`, `SaveContext`, `Renew`, `Destroy`) with `scs.LoadAndSave` middleware and context helpers. This represents a public ABI break under HISS-14, requiring migration across all downstream consumers and internal callers. Furthermore, wrapping `scs` to comply with repository standards requires external shims for clock injection (not supported upstream due to hardcoded `time.Now()`) and an `ErrorFunc` override routing errors to `core/log`.

### Option b: Adapter between scs Store and our engine

Wrapping `scs.SessionManager` or bridging `scs.Store` / `scs.CtxStore` behind the existing `auth/session.Manager` contract preserves the public ABI, but adds a third-party dependency with no reduction in codebase complexity. The adapter layer needed to translate `map[string]any` to raw bytes, mock or work around ambient context middleware, and bypass premature token deletion is estimated (not measured) at more than 150 LOC, yielding negligible maintenance benefit over the 336 LOC native implementation.

### Option c: Keep hand-written engine (chosen)

Retain the native implementation in `auth/session`. At 336 LOC, the package is minimal, fully understood, and tailored to golusoris architecture:

- Directly accepts `clockwork.Clock` in `NewMemoryStoreWithClock` for deterministic testing.
- Defaults cookies to `Secure: true`, `HttpOnly: true`, and `SameSite: Lax`.
- Renews tokens save-before-delete: the old token stays valid until the new one is persisted and the cookie is set.
- Needs no public API change under HISS-14 and adds no module.

## Decision

Keep the hand-written session engine in `auth/session` and decline the adoption of `github.com/alexedwards/scs/v2`.

## Consequences

- **Positive**: Zero supply chain additions; module count remains at 603 without adding `alexedwards/scs/v2`.
- **Positive**: Maintains strict adherence to `AGENTS.md` hard rules 6 (`core/clock` deterministic clockwork injection) and 7 (the engine emits no logs, so no global logger).
- **Positive**: Preserves secure cookie defaults (`Secure: true`) and save-before-delete `Renew` semantics preventing session loss on write failures.
- **Positive**: Zero public ABI churn for consumers of `auth/session`.
- **Negative**: Maintainers retain ownership of session storage abstractions and future backend store implementations.
- **Revisit triggers**: Re-evaluate Option b if upstream `scs` gains an injectable clock abstraction and a slog-friendly default error hook, OR if the golusoris engine requirements expand to include complex features (such as dual idle and absolute timeouts or multi-backend stores) well beyond its current size.

## References

- Issue #1183 (auth/session: evaluate alexedwards/scs/v2 vs hand-written engine).
- ADR-0023: Keep hand-written OAuth2 authorization server.
- [`auth/session/session.go`](https://github.com/golusoris/golusoris/blob/main/auth/session/session.go) (hand-written engine implementation).
- [`auth/session/session_test.go`](https://github.com/golusoris/golusoris/blob/main/auth/session/session_test.go) (clockwork deterministic expiry tests).
- Upstream [`alexedwards/scs`](https://github.com/alexedwards/scs).
