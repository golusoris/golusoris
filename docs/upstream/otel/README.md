# go.opentelemetry.io/otel — v1.35.0 snapshot

Pinned: **v1.35.0** (semconv: v1.26.0)
Source: https://pkg.go.dev/go.opentelemetry.io/otel@v1.35.0

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

counter, _ := meter.Int64Counter("requests_total",
    metric.WithDescription("Total requests"),
    metric.WithUnit("{request}"))
counter.Add(ctx, 1, metric.WithAttributes(attribute.String("method", "GET")))

histogram, _ := meter.Float64Histogram("request_duration_seconds")
histogram.Record(ctx, duration.Seconds())
```

## SDK setup (OTLP exporter)

```go
import (
    "go.opentelemetry.io/otel/sdk/trace"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

exp, _ := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint("localhost:4317"))
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

- `otel/` — SDK init + OTLP exporter provided via fx; sets global tracer + meter providers.
- `log/` — OTel log bridge via `go.opentelemetry.io/contrib/bridges/otelslog`.

## Links

- Spec: https://opentelemetry.io/docs/specs/otel/
- Semconv v1.26: https://opentelemetry.io/docs/specs/semconv/
- Changelog: https://github.com/open-telemetry/opentelemetry-go/blob/main/CHANGELOG.md
