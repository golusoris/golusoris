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
- `k8s/health.Module` wired -> stop hook wrapped by `*health.ShutdownGate`: server keeps serving (`/readyz` = 503) for `health.drain.delay`, then `Shutdown` bounded by `http.timeouts.shutdown` + stop ctx.
- Uses `net.Listen` explicitly so `:0` picks free port for tests; `srv.Serve` runs in goroutine so fx Start isn't blocked.
