<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — observability/rules

Prometheus alerting + recording rules in Go: `monitoring.coreos.com/v1` PrometheusRule (prometheus-operator types, v0.94.1) or plain rule file for `promtool`. Runbook URL mandatory per alert. Stateless; no fx module.

## API

| Symbol | Purpose |
| --- | --- |
| `Alert{Name, Expr, For, Severity, Summary, Description, RunbookURL, Labels, Annotations}` | alerting rule; `Validate()` |
| `Record{Name, Expr, Labels}` | recording rule; name must be `level:metric:operations` (contains colon) |
| `Group{Name, Interval, Records, Alerts}` | rule group; records render before alerts; `RuleGroup()` -> operator type |
| `NewPrometheusRule(meta, groups...)` / `RenderPrometheusRule` | CR object / YAML (no status, no creationTimestamp) |
| `RenderRuleFile(groups...)` | plain `groups:` YAML for `promtool check rules` / `promtool test rules` |
| `SLO{Name, AlertName, Objective, SLI, RunbookURL, Labels}.Group()` | multi-window multi-burn-rate SLO group |
| `EventsSLI{Errors, Total}` / `LatencySLI{Histogram, Matchers, Threshold}` | error-ratio sources; `SLI` interface for custom |
| `RecordName(window)` | `slo:sli_error:ratio_rate<window>` |

## Rules enforced

- alert: name `[A-Za-z][A-Za-z0-9_]*`, expr + summary required, severity `critical|warning|info`, runbook absolute http(s) URL, `For` whole ms >= 0.
- builder owns `severity` label + `summary`/`description`/`runbook_url` annotations; setting them in maps fails.
- group: name required, >= 1 rule, unique names per render.
- PromQL syntax not parsed here (no prometheus/prometheus dep in root); `testutil/promcheck` parses + checks metric names.

## SLO

- Records error ratio over 5m, 30m, 1h, 2h, 6h, 1d, 3d with label `slo=<Name>`.
- `<AlertName>ErrorBudgetBurnFast` (critical): 1h & 5m > 14.4x budget OR 6h & 30m > 6x. `<AlertName>ErrorBudgetBurnSlow` (warning): 1d & 2h > 3x OR 3d & 6h > 1x (SRE workbook). No `for`: short window gives fast reset.
- `LatencySLI.Threshold` must equal bucket bound; integer bounds match `le="1"` and `le="1.0"` (classic vs OpenMetrics / Prometheus 3).
- Zero error series -> no ratio series -> no alert (absent, not 0).

## Don't

- Don't ship alert without runbook page — builder refuses anyway.
- Don't hand-write `PrometheusRule` YAML next to builder output; promcheck only sees what tests feed it.
