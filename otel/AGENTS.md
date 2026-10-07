<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — otel

Full OpenTelemetry SDK — tracer, meter, logger — with OTLP gRPC exporter.

## Conventions

- Apps wire `otel.Module`. SDK registers itself as OTel global, so `httpx/middleware.OTel`, `httpx/client` (via otelhttp), and any package using `otel.Tracer("...")` get spans automatically.
- `otel.service.name` defaults to binary's name when unset: the build-info path's last segment (`runtime/debug.ReadBuildInfo().Path`), else `os.Args[0]` basename, with any platform extension stripped. Set `otel.service.name` to override. loader only errors when enabled and name can be neither configured nor derived — so shared bootstrap enables OTel fleet-wide with zero per-binary config (issue #254). module degrades to silent no-op (empty `Providers`, global OTel stays no-op, no exporter, no network) when `otel.enabled=false`, `OTEL_SDK_DISABLED=true`, or no OTLP endpoint is configured — neither `otel.endpoint` nor any `OTEL_EXPORTER_OTLP_*_ENDPOINT` env var. last case is 12-factor default: no collector wired → no-op.
- Resource attrs include service.{name,version,namespace}, process attrs, and k8s pod metadata from downward API (POD_NAME, POD_NAMESPACE, POD_IP, NODE_NAME, SERVICE_ACCOUNT) — same set `core/log/` package reads.
- Per-signal toggles: `otel.export.{traces,metrics,logs}`. Useful when collector rejects one signal type, or when app traffic is noisy along one axis.
- Provider construction is atomic: globals change after every enabled signal builds. Partial failures shut down built providers.
- Shutdown swaps still-owned global SDK providers for no-op providers before exporter teardown. Newer globals remain intact.

## Prometheus pull

- `otel.export.prometheus=true` adds `go.opentelemetry.io/otel/exporters/prometheus` reader: every OTel instrument (otelhttp, otelgrpc, app meters) appears on app's Prometheus registry. Needs no OTLP endpoint; traces + logs still need one.
- Registry: fx-provided `prometheus.Registerer` when present, else `prometheus.DefaultRegisterer`. Non-fx: `otel.NewWithRegisterer(ctx, opts, reg)`.
- Naming = exporter default (underscore escaping, unit + `_total` suffixes): `http.server.request.duration` (s) -> `http_server_request_duration_seconds_{bucket,sum,count}`, attr `http.response.status_code` -> label `http_response_status_code`; series carry `otel_scope_*` labels; resource -> `target_info`.
- Serve via `prom.OpenMetricsHandler(reg)`: sampled-span exemplars (`trace_id`, `span_id`) ride OpenMetrics only.
- Collector stays registered after `Shutdown`; shut-down reader makes it silent.

## Logs

- `otel.Module` contributes `TraceHandler` to `core/log` middleware group: injected `*slog.Logger` (and `slog.Default`) stamps `trace_id`, `span_id`, `trace_flags` (consts `TraceIDKey`, `SpanIDKey`, `TraceFlagsKey`) on records whose ctx carries valid span. Config `otel.logs.trace_ids` (default true; effective only with `otel.enabled`). Env override needs `config.Options.CompoundKeys: []string{"otel.logs.trace_ids"}` -> `APP_OTEL_LOGS_TRACE_IDS`.
- Use `*Context` slog calls; plain `Info` has no ctx -> no IDs. `WithGroup` nests trace keys under open group.
- `otel.Module` invokes provider construction at start; globals go live without explicit `*Providers` consumer.
- `otel.ModuleWithSlogBridge` contributes OTLP bridge (otelslog fan-out) to same group, ordered outside trace middleware: stdout gets trace keys, OTLP records keep native span context. With app-supplied `*slog.Logger` (no `core/log.Module`) only `slog.Default` gains bridge.

## Don't

- Don't set sample ratio to 1.0 in production under high traffic. Start at 0.1 or lower and tune from observed tail-latency coverage.
- Don't wire two OTel modules — global provider can only be one.
