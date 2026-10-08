<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — observability/grafana

Grafana dashboards generated in Go from `observability/metricdef` defs, on `github.com/grafana/grafana-foundation-sdk/go` (pinned v0.0.20, zero transitive deps). Stateless; no fx module.

## API

| Symbol | Purpose |
| --- | --- |
| `NewDashboard(uid, title, tags...)` | `*dashboard.DashboardBuilder` + `${datasource}` variable, crosshair, 30s refresh, 6h window |
| `Render(b)` | build + indented JSON (`[]byte`, trailing newline) for provisioning |
| `Query{Def, By, Filters, Matchers}` | PromQL source; every label must be in `Def.Labels` |
| `RateExpr` / `IncreaseExpr` / `QuantileExpr` / `ValueExpr` / `RatioExpr` | PromQL strings; kind-checked |
| `RatePanel` / `QuantilePanel(q, o, phis...)` / `ValuePanel` / `RatioPanel` / `StatPanel` | panel builders; chain further SDK methods (`.Height`, `.Span`) |
| `PanelOptions{Title, Description, Unit, Legend, Thresholds, Links}` | zero values fall back to Def |
| `LabelVariable(def, label)` | multi-select `label_values` variable, All = `.*` |
| `Annotation(name, expr, color)` | Prometheus annotation (deploys, model changes) |
| `Links(...Link)` / `DashboardURL(uid)` / `Thresholds(...Threshold)` | drill-down + colour steps |
| `ValueUnit(u)` / `RateUnit(u)` | metricdef unit -> Grafana unit |

## Conventions

- `Filters` render `label=~"$label"`; pair each with `LabelVariable(def, label)` so `$label` exists.
- Rate windows use `$__rate_interval`; increase stats use `$__range`.
- Quantile targets set `exemplar: true` -> latency points open traces (needs OTel Prometheus export + `prom.OpenMetricsHandler`).
- Classic v1 JSON model on purpose (`//nolint:staticcheck` on SDK deprecation): sidecar ConfigMap provisioning + dashboard-linter read v1.
- Verify rendered JSON in tests with `testutil/promcheck` against metric catalog.

## Usage

```go
q := grafana.Query{Def: JobLatency, By: []string{"backend"}, Filters: []string{"tenant"}}
tenant, err := grafana.LabelVariable(JobLatency, "tenant")
p99, err := grafana.QuantilePanel(q, grafana.PanelOptions{Thresholds: []grafana.Threshold{{Value: 5, Color: "red"}}}, 0.5, 0.99)
raw, err := grafana.Render(grafana.NewDashboard("vmafx-overview", "VMAFx overview").WithVariable(tenant).WithPanel(p99))
```

## Don't

- Don't hand-write PromQL strings into panels when `Query` covers it; hand strings bypass label checks.
- Don't change dashboard `uid` once shipped — links + provisioning key on it.
- Don't add vendor-exporter panels unconditionally; gate them in generator on exporter presence.
