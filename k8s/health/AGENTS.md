<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/health

`/livez` `/readyz` `/startupz` handlers, backed by `statuspage.Registry`.

## Conventions

- One Registry per app. Register checks once, tag for purpose:
- `health.TagLiveness` — process not deadlocked. Cheap, almost-always-up. Examples: counter that increments per second; goroutine heartbeat.
- `health.TagReadiness` — deps reachable. DB ping, cache ping, downstream API health.
- `health.TagStartup` — one-time init complete. Migrations applied, caches warmed.
- Probe responses default to plain `ok\n` / `not ok\n` — k8s only inspects status code. `?verbose=1` returns JSON for human debugging.
- Untagged checks appear on `/status` only; never on probes (prevents heavy diagnostics from blocking k8s).
- Each check has 2s per-call timeout (statuspage default). Keep checks fast — long checks cascade into probe failures.

## Graceful drain

- `health.Module` -> `*ShutdownGate` from `health.drain.delay` (env `APP_HEALTH_DRAIN_DELAY`; default 5s; 0 = no wait; negative rejected). Registers readiness check `shutdown` on app `*statuspage.Registry` (app supplies: `fx.Provide(statuspage.NewRegistry)`).
- fx Stop: gate fails `/readyz` (503), holds stop for drain window, then servers drain. `/livez` + `/startupz` unaffected.
- fx runs OnStop in reverse append order -> module order alone can't put gate first. `httpx/server` + `grpc` inject gate optional, wrap stop hook via `gate.Wrap(hook)` -> order-independent. App-owned servers: `lc.Append(gate.Wrap(fx.Hook{...}))`.
- Drain bounded by stop ctx. Ctx shorter than delay -> wrapped ctx error; wrapped OnStop still runs (best effort).
- Budget: `fx.StopTimeout` (default 15s) > drain delay + `http.timeouts.shutdown`. Helm `terminationGracePeriodSeconds` > preStop sleep + drain delay + shutdown.
- `jobs` not wrapped: river `Stop(ctx)` bounded by stop ctx; no routed traffic.

## Dependency readiness

- `health.DependencyCheck(name, timeout, logger, probe)` -> readiness-tagged check; probe ctx bounded by timeout (<= 0 -> `DefaultDependencyTimeout` 1s, under registry 2s). Failure -> `ErrDependencyNotReady` ("timed out" or "failed"); cause logged only, never on `/status`.
- Opt-in per dependency, owner package: `pgx.ReadinessModule` (Ping), `redis.ReadinessModule` (PING), `nats.ReadinessModule` (CONNECTED + flush). Each also exports `ReadinessCheck(...)`.
- Shared dependency down -> every replica unready. Opt in only when pod can't serve without it.

## Probe semantics (k8s docs)

- `livenessProbe` failure → kubelet restarts container.
- `readinessProbe` failure → endpoint removed from Service load balancing.
- `startupProbe` runs first; only after it passes do liveness + readiness probes start.

## Don't

- Don't mix tags across endpoints intentionally — `/livez` should NOT depend on the database. A flaky DB shouldn't restart pod.
- Don't return non-2xx for transient flaps. Use circuit breaker / debounce in check function if needed.
