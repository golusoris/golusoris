<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — observability/metricdef

One metric definition shared by services, dashboard generator (`observability/grafana`), rule builder (`observability/rules`) and query checks (`testutil/promcheck`). Stateless; no fx module.

## API

| Symbol | Purpose |
| --- | --- |
| `Def{Name, Help, Unit, Kind, Labels, Buckets, Limits, External}` | one metric; `Validate()` reports every violation |
| `KindCounter` / `KindGauge` / `KindHistogram` / `KindSummary` | metric type; summary External-only (Go collector), instrument with histograms |
| `LabelLimit{Allow}` / `LabelLimit{MaxDistinct}` | cardinality bound; rejected value -> `OtherValue` (`"other"`) |
| `NewCatalog(defs...)` | validated immutable set; rejects duplicates + OpenMetrics family clash (`x_total` vs gauge `x`) |
| `Catalog.Defs()` / `Lookup` / `LookupSeries` / `Series` / `Merge` | generator + checker reads; deep copies |
| `Catalog.Register(reg)` / `Register(reg, defs...)` | all-or-nothing registration -> `*Handles`; `External` defs skipped / refused |
| `Catalog.RegisterOrReuse(reg)` / `RegisterOrReuse(reg, defs...)` | register or reuse existing collectors -> `*Handles`; separate guards |
| `Catalog.RegisterOTel(m)` / `RegisterOTel(m, defs...)` | OTel synchronous float64 instruments; same handles + label guard; catalog skips External, standalone refuses |
| `Handles.Counter/Gauge/OTelGauge/Histogram(name)` | typed handle or error |
| `Counter.Add(ctx, v, labels...)`, `Inc` | sampled span in ctx -> exemplar |
| `Gauge.Set(v, labels...)`, `Add`, `Reset`, `Delete` | no exemplars; `Reset` clears series + guards; `Delete` removes series |
| `OTelGauge.Set(v, labels...)`, `Add` | OTel-backed gauge; no `Reset`, no `Delete` |
| `Histogram.Observe(ctx, v, labels...)` | exemplar via `prom.ObserveWithExemplar` |
| `Def.Series()` / `Def.SeriesLabels(series)` | exposed sample names; `le` only on `_bucket`, `quantile` only on summary base series |

## Naming rules (Validate)

- name `[a-zA-Z_][a-zA-Z0-9_]*`, no leading `__`, no colons (recording rules own colons).
- counter ends `_total`; gauge/histogram never. `_bucket`/`_count`/`_sum`/`_created` reserved.
- `Unit` = base unit, name ends `_<unit>` (before `_total`); `milliseconds`, `percent`, `kilobytes`... rejected.
- labels valid, unique, no `__` prefix; `quantile` reserved; `le` reserved on histograms.
- buckets only on histograms, finite, strictly increasing.
- each `Limits` key = declared label; exactly one of `Allow` / positive `MaxDistinct`.
- `External` defs: syntax only (name may hold colons, help optional, no suffix/unit rules, no family-clash check) — emitter owns naming. Exposed-series collisions still fail.

## OTel backend

- Counter `_total` + unit suffix stripped before instrument creation; exporter restores exact declared name.
- Units: `seconds` -> `s`, `bytes` -> `By`, `ratio` -> `1`; others pass only on exact exporter round-trip.
- Ratio unit: gauges only; counters + histograms fail registration. Exporter-normalized names fail instead of silently changing.
- Histogram boundaries: `Def.Buckets`; empty -> `prometheus.DefBuckets`.
- Gauge Set/Add: mutex-protected state per bounded attribute set; synchronous Record, background context.
- OTel meter: no unregister API; validation + name mapping precede creation, later meter errors cannot roll back instruments.
- Prometheus backend retains sampled-span exemplars; OTel exemplar policy belongs to SDK.

## Usage

```go
var Catalog = must(metricdef.NewCatalog(
    metricdef.Def{Name: "vmafx_jobs_total", Help: "Finished jobs.", Kind: metricdef.KindCounter,
        Labels: []string{"tenant", "outcome"},
        Limits: map[string]metricdef.LabelLimit{"tenant": {MaxDistinct: 200}, "outcome": {Allow: []string{"ok", "failed"}}}},
    metricdef.Def{Name: "http_server_request_duration_seconds", Help: "otelhttp", Unit: "seconds",
        Kind: metricdef.KindHistogram, Labels: []string{"http_route", "http_response_status_code"}, External: true},
))
h, err := Catalog.Register(promReg)
jobs, err := h.Counter("vmafx_jobs_total")
err = jobs.Inc(ctx, tenant, "ok")
```

## Don't

- Don't call `prometheus.NewCounterVec` beside catalog — dashboards + promcheck never see it.
- Don't put unbounded ids (job id, user id, path) in labels; `MaxDistinct` folds overflow, but first N values win per process.
- Don't register `External` metrics (otelhttp, Go collector) — declare for generators only.
