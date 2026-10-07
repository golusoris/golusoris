<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/metrics/prom

Prometheus `/metrics` endpoint with Go runtime + process collectors and gauge per registered statuspage check.

## Conventions

- `prom.Mount(r, reg) error` mounts `/metrics` on r and (if reg non-nil) wires
 `app_check_status{name="<check>"}` (1 = up, 0 = down) +
 `app_check_latency_seconds{name="<check>"}`. Gauges refresh on every
 `reg.Run` / `reg.RunTagged` invocation — Prometheus scrapes latest
 snapshot. `prom.MountFor(mux, promReg, checks) error` is custom-registry
 / net/http variant. Both return error only when gauge registration fails
 for reason other than "already registered".
- App-defined collectors register on `prometheus.DefaultRegisterer`.
 Custom Registry instances are supported via `prom.HandlerFor(reg)` —
 not exposed yet; add when needed.

## Exemplars

- `prom.OpenMetricsHandler(g)` = `promhttp.HandlerFor` with `EnableOpenMetrics`; only OpenMetrics carries exemplars. Classic-text scrapers unaffected. OpenMetrics renders integer `le`/`quantile` as `"1.0"`: Prometheus 3 normalises, Prometheus 2 sees new series once.
- `prom.ObserveWithExemplar(ctx, obs, v)` attaches `trace_id`/`span_id` (`prom.TraceIDLabel`, `prom.SpanIDLabel`) when ctx span sampled + obs implements `prometheus.ExemplarObserver`; else plain `Observe`. `prom.ExemplarLabels(ctx)` returns labels or nil.
- `Handler()` / `HandlerFor()` / `Mount*` keep classic negotiation (no silent format switch for existing scrapes).

## Don't

- Don't use per-test `MustRegister` without recovery — global
 default registry persists across tests. package's `registerCheckStatusOn`
 accepts `prometheus.AlreadyRegisteredError` so repeat `Mount` calls in tests
 don't fail.
