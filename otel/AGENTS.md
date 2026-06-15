# Agent guide — otel

Full OpenTelemetry SDK — tracer, meter, logger — with an OTLP gRPC exporter.

## Conventions

- Apps wire `otel.Module`. The SDK registers itself as the OTel global, so `httpx/middleware.OTel`, `httpx/client` (via otelhttp), and any package using `otel.Tracer("...")` get spans automatically.
- `otel.service.name` defaults to the binary's name when unset: the build-info path's last segment (`runtime/debug.ReadBuildInfo().Path`), else the `os.Args[0]` basename, with any platform extension stripped. Set `otel.service.name` to override. The loader only errors when enabled and the name can be neither configured nor derived — so a shared bootstrap enables OTel fleet-wide with zero per-binary config (issue #254). The module degrades to a silent no-op (empty `Providers`, global OTel stays no-op, no exporter, no network) when `otel.enabled=false`, `OTEL_SDK_DISABLED=true`, or no OTLP endpoint is configured — neither `otel.endpoint` nor any `OTEL_EXPORTER_OTLP_*_ENDPOINT` env var. The last case is the 12-factor default: no collector wired → no-op.
- Resource attrs include service.{name,version,namespace}, process attrs, and k8s pod metadata from the downward API (POD_NAME, POD_NAMESPACE, POD_IP, NODE_NAME, SERVICE_ACCOUNT) — same set the `log/` package reads.
- Per-signal toggles: `otel.export.{traces,metrics,logs}`. Useful when the collector rejects one signal type, or when app traffic is noisy along one axis.

## slog bridge

`otel.ModuleWithSlogBridge` installs a fanout slog handler that writes to both the local handler (tint/JSON) and the OTel logger provider. Apps that want every slog call exported as an OTel log record include this module in addition to `otel.Module`.

## Don't

- Don't set sample ratio to 1.0 in production under high traffic. Start at 0.1 or lower and tune from observed tail-latency coverage.
- Don't wire two OTel modules — the global provider can only be one.
