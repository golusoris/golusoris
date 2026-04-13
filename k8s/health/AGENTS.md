# Agent guide — k8s/health

`/livez` `/readyz` `/startupz` handlers, backed by `statuspage.Registry`.

## Conventions

- One Registry per app. Register checks once, tag for purpose:
  - `health.TagLiveness` — process not deadlocked. Cheap, almost-always-up. Examples: a counter that increments per second; a goroutine heartbeat.
  - `health.TagReadiness` — deps reachable. DB ping, cache ping, downstream API health.
  - `health.TagStartup` — one-time init complete. Migrations applied, caches warmed.
- Probe responses default to plain `ok\n` / `not ok\n` — k8s only inspects status code. `?verbose=1` returns JSON for human debugging.
- Untagged checks appear on `/status` only; never on probes (prevents heavy diagnostics from blocking k8s).
- Each check has a 2s per-call timeout (statuspage default). Keep checks fast — long checks cascade into probe failures.

## Probe semantics (k8s docs)

- `livenessProbe` failure → kubelet restarts the container.
- `readinessProbe` failure → endpoint removed from Service load balancing.
- `startupProbe` runs first; only after it passes do liveness + readiness probes start.

## Don't

- Don't mix tags across endpoints intentionally — `/livez` should NOT depend on the database. A flaky DB shouldn't restart the pod.
- Don't return non-2xx for transient flaps. Use a circuit breaker / debounce in the check function if needed.
