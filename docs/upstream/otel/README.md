<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# go.opentelemetry.io/otel — v1.46.0 snapshot

Pinned: **v1.46.0** (semconv API: **v1.26.0**)
Source: [tagged source](https://github.com/open-telemetry/opentelemetry-go/tree/v1.46.0)

## Tracer

```go
import "go.opentelemetry.io/otel"

tracer := otel.Tracer("github.com/golusoris/golusoris/mypackage")

ctx, span := tracer.Start(ctx, "operation-name")
defer span.End()

span.SetAttributes(attribute.String("key", "value"))
span.RecordError(err)
span.SetStatus(codes.Error, "something went wrong")
```

## Meter

```go
import "go.opentelemetry.io/otel/metric"

meter := otel.Meter("github.com/golusoris/golusoris/mypackage")

counter, err := meter.Int64Counter("requests_total",
    metric.WithDescription("Total requests"),
    metric.WithUnit("{request}"))
if err != nil {
    return fmt.Errorf("create request counter: %w", err)
}
counter.Add(ctx, 1, metric.WithAttributes(attribute.String("method", "GET")))

histogram, err := meter.Float64Histogram("request_duration_seconds")
if err != nil {
    return fmt.Errorf("create request histogram: %w", err)
}
histogram.Record(ctx, duration.Seconds())
```

## SDK setup (OTLP exporter)

```go
import (
    "go.opentelemetry.io/otel/sdk/trace"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint("localhost:4317"))
if err != nil {
    return fmt.Errorf("create OTLP trace exporter: %w", err)
}
tp := trace.NewTracerProvider(
    trace.WithBatcher(exp),
    trace.WithResource(resource.NewWithAttributes(
        semconv.SchemaURL,
        semconv.ServiceName("my-service"),
        semconv.ServiceVersion("v1.0.0"),
    )),
)
otel.SetTracerProvider(tp)
```

Own `tp.Shutdown(ctx)` in an Fx lifecycle hook so queued spans are flushed on
shutdown.

## Semantic conventions (v1.26)

```go
import semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

semconv.ServiceName("my-service")
semconv.HTTPRequestMethodKey.String("GET")
semconv.HTTPResponseStatusCodeKey.Int(200)
semconv.DBSystemPostgreSQL
semconv.MessagingSystemKafka
```

## Context propagation

```go
import "go.opentelemetry.io/otel/propagation"

otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
    propagation.TraceContext{},
    propagation.Baggage{},
))

// Inject (outbound)
otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

// Extract (inbound)
ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(req.Header))
```

## golusoris usage

- `otel/` — SDK initialization and OTLP exporter provided via Fx; sets global
  tracer and meter providers.
- `core/log/` — slog factory; `otel.ModuleWithSlogBridge` installs the OTel
  bridge via `go.opentelemetry.io/contrib/bridges/otelslog`.
- `otel.export.prometheus` adds `go.opentelemetry.io/otel/exporters/prometheus`
  v0.68.0 (the release built against v1.46.0) as a pull reader on the app's
  Prometheus registry.

## Links

- [OpenTelemetry specification](https://opentelemetry.io/docs/specs/otel/)
- [Semantic conventions](https://opentelemetry.io/docs/specs/semconv/)
- [Changelog](https://github.com/open-telemetry/opentelemetry-go/blob/v1.46.0/CHANGELOG.md)
