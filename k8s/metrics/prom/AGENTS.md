<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/metrics/prom

Prometheus `/metrics` endpoint with Go runtime + process collectors and a
gauge per registered statuspage check.

## Conventions

- `prom.Mount(r, reg) error` mounts `/metrics` on r and (if reg non-nil) wires
  `app_check_status{name="<check>"}` (1 = up, 0 = down) +
  `app_check_latency_seconds{name="<check>"}`. Gauges refresh on every
  `reg.Run` / `reg.RunTagged` invocation — Prometheus scrapes the latest
  snapshot. `prom.MountFor(mux, promReg, checks) error` is the custom-registry
  / net/http variant. Both return an error only when gauge registration fails
  for a reason other than "already registered".
- App-defined collectors register on `prometheus.DefaultRegisterer`.
  Custom Registry instances are supported via `prom.HandlerFor(reg)` —
  not exposed yet; add when needed.

## Don't

- Don't use a per-test `MustRegister` without recovery — the global
  default registry persists across tests. The package's `registerCheckStatusOn`
  accepts `prometheus.AlreadyRegisteredError` so repeat `Mount` calls in tests
  don't fail.
