# Agent guide — otel

Full OpenTelemetry SDK — tracer, meter, logger — with an OTLP gRPC exporter.

## Conventions

- Apps wire `otel.Module`. The SDK registers itself as the OTel global, so `httpx/middleware.OTel`, `httpx/client` (via otelhttp), and any package using `otel.Tracer("...")` get spans automatically.
- `otel.service.name` is required when enabled. Disable via `otel.enabled=false` to get a no-op install.
- Resource attrs include service.{name,version,namespace}, process attrs, and k8s pod metadata from the downward API (POD_NAME, POD_NAMESPACE, POD_IP, NODE_NAME, SERVICE_ACCOUNT) — same set the `log/` package reads.
- Per-signal toggles: `otel.export.{traces,metrics,logs}`. Useful when the collector rejects one signal type, or when app traffic is noisy along one axis.

## slog bridge

`otel.ModuleWithSlogBridge` installs a fanout slog handler that writes to both the local handler (tint/JSON) and the OTel logger provider. Apps that want every slog call exported as an OTel log record include this module in addition to `otel.Module`.

## Don't

- Don't set sample ratio to 1.0 in production under high traffic. Start at 0.1 or lower and tune from observed tail-latency coverage.
- Don't wire two OTel modules — the global provider can only be one.
