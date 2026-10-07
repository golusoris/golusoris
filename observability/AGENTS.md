<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — observability/

Sub-packages layering on top of `otel/`:

| Subpackage | Purpose |
| --- | --- |
| `observability/sentry` | Sentry client + slog bridge (errors → events, warns → breadcrumbs) |
| `observability/profiling` | Pyroscope in-process profiling |
| `observability/pprof` | Auth-gated `/debug/pprof` handler |
| `observability/statuspage` | HTML + JSON `/status` page backed by shared check registry |
| `observability/metricdef` | metric catalog: `Def` -> typed handles + generator/checker input |
| `observability/grafana` | dashboard JSON generator over metricdef (Foundation SDK) |
| `observability/rules` | PrometheusRule / rule-file builder, runbook required, SLO burn-rate alerts |

## Conventions

- Every module is off-by-default unless explicitly enabled. Aggregate cost of
  accidentally wiring all modules into idle app is zero. Disabled modules
  allocate no resources.
- Error reporting: `slog.Error` → routed to Sentry (via this package's bridge) + OTel logs (via `otel.ModuleWithSlogBridge`). Apps wire BOTH bridges; fanout handler supports it.
