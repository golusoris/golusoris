<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/promcheck/

Test gate: fail when dashboard panel, template variable, annotation or Prometheus rule queries metric or label nothing emits. Parses PromQL with upstream `prometheus/prometheus/promql/parser` (v0.315.0). Stateless test utility — no fx wiring.

## API

| Symbol | Purpose |
|---|---|
| `DashboardExprs(json, source)` | Grafana classic JSON (or API `{"dashboard": …}` wrapper) -> `[]Expr`: panel targets incl. collapsed rows, query variables, annotations; non-Prometheus datasources skipped |
| `RuleExprs(yaml, source)` | PrometheusRule CR (`spec.groups`) or plain rule file, multi-doc `---` ok -> `[]Expr`; alerts carry `$labels.x` refs + static labels |
| `Check(exprs, catalog, opts...)` | `[]Finding`: unknown metric, label matcher not on metric, `by()` label on no aggregated series, `$labels.x` not on result, parse error, nameless selector |
| `AssertKnownMetrics(t, exprs, catalog, opts...)` | `t.Errorf` per finding; `t` = any `Helper()+Errorf` (`*testing.T`, fakes) |
| `CatalogFromGatherer(g)` | catalog of External defs from real emission (`prometheus.Gatherer`) |
| `WithTargetLabels(...)` | scrape-time labels (namespace, pod, …); `job`, `instance` always allowed |
| `WithOpenSeries(...)` | series outside catalog accepted with any label |
| `TemplateLabels(texts...)` | `$labels.x` names in templates |

## Semantics

- Grafana vars outside quotes -> stand-ins (`$__rate_interval` -> `5m`, `$__range_s` -> `300`, `[$var]` -> `5m`, else `1`); vars inside matcher strings stay literal.
- `label_values(sel, label)` -> checked as `count by (label) (sel)`; `label_values(label)` skipped.
- Recording rules among exprs make `record:` names known, any labels. Synthetic `up`, `scrape_*`, `ALERTS*` always known.
- `le` only on `_bucket`; `quantile` only on summary base series. `label_replace`/`label_join` destination label counts for enclosing `by()`.
- `$labels.x` check: result labels approximated — `by()` aggregation = grouping, selector = its labels, else union of all selectors.
- Regex `__name__` matchers: some known series must match; labels then unchecked.

## Gate in this repo

- `deploy_test.go`: emission catalog = Go + process collectors, chi router with `middleware.OTel` via OTel Prometheus reader, check gauges -> `deploy/observability/*` must be clean.
- `testdata/prometheus-rules-shipped-8a0dd70.yaml` = planted defect (`status` filter, `$labels.check`) -> must yield exactly 3 findings.

## Notes

- **Own go.mod sub-module** (`github.com/golusoris/golusoris/testutil/promcheck`): prometheus/prometheus pulls ~250 modules; kept out of root. Local `replace` to `../..` + `../../core`.
- dashboard-linter (grafana/dashboard-linter v0.3.0) not embedded — pulls Loki + Grafana apps. Run as CLI in consumer CI.

## Don't

- Don't build catalog from emission without exercising code paths first — families without samples have no labels.
- Don't widen `WithTargetLabels` to silence real findings; add metric to catalog or fix query.
