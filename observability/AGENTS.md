# Agent guide — observability/

Sub-packages layering on top of `otel/` (Step 5a):

| Subpackage | Purpose |
|---|---|
| `observability/sentry` | Sentry client + slog bridge (errors → events, warns → breadcrumbs) |
| `observability/profiling` | Pyroscope in-process profiling |
| `observability/pprof` | Auth-gated `/debug/pprof` handler |
| `observability/statuspage` | HTML + JSON `/status` page backed by a check registry |

## Conventions

- Every module is off-by-default unless explicitly enabled. The aggregate cost of accidentally wiring all of them on an idle app is zero.
- Error reporting: `slog.Error` → routed to Sentry (via this package's bridge) + OTel logs (via `otel.ModuleWithSlogBridge`). Apps wire BOTH bridges; the fanout handler supports it.
