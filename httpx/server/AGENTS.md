<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/server

Wraps `*http.Server` with slow-loris guards, body limits, graceful shutdown.

## Conventions

- Requires `http.Handler` in fx graph (from `httpx/router` or ogen handler chain).
- All timeouts are opinionated non-zero defaults to protect against slow-loris + resource exhaustion. Apps tune via `http.timeouts.*` / `http.limits.*`.
- Body limit default = 10 MiB. `Limits.AllowUnlimitedBody` = explicit unbounded opt-in for streaming endpoints.
- `k8s/health.Module` wired -> stop hook wrapped by its `core/drain.Gate` (no `k8s/health` import here): server keeps serving (`/readyz` = 503) for `health.drain.delay`, then `Shutdown` bounded by `http.timeouts.shutdown` + stop ctx.
- Uses `net.ListenConfig.Listen` with start ctx so `:0` picks free port for tests; `srv.Serve` runs in goroutine so fx Start isn't blocked.
- TLS sources: optional autotls `*tls.Config` in graph, or `http.tls.cert` + `http.tls.key` (+ `http.tls.ca`, `http.tls.clientauth` for mTLS). Both at once fails construction. Neither = plaintext.
- File TLS uses `core/tlsx`: TLS 1.3, files reload on handshake (30s min interval, optional `clock.Clock` from graph), bad rotation keeps last good cert. `NextProtos` = `h2`, `http/1.1`.
- `clientauth` modes: `none`, `request`, `require_any`, `verify_if_given`, `require_and_verify`. CA without mode = `require_and_verify`; verifying mode without CA fails construction. Key is one word: env split rule.
