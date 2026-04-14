# prometheus/client_golang — v1.22.0 snapshot

Pinned: **v1.22.0**
Source: https://pkg.go.dev/github.com/prometheus/client_golang@v1.22.0

## Metrics registration

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

// promauto registers on the default registry automatically
var (
    httpRequests = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "http_requests_total",
        Help: "Total HTTP requests.",
    }, []string{"method", "path", "status"})

    requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "http_request_duration_seconds",
        Help:    "HTTP request duration.",
        Buckets: prometheus.DefBuckets,
    }, []string{"method", "path"})

    activeConnections = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "active_connections",
        Help: "Current active connections.",
    })
)

// Observe
httpRequests.WithLabelValues("GET", "/users", "200").Inc()
requestDuration.WithLabelValues("GET", "/users").Observe(duration.Seconds())
activeConnections.Set(float64(count))
```

## HTTP handler

```go
import "github.com/prometheus/client_golang/prometheus/promhttp"

r.Handle("/metrics", promhttp.Handler())
// Or with custom registry:
r.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
```

## Custom registry

```go
reg := prometheus.NewRegistry()
reg.MustRegister(
    collectors.NewGoCollector(),
    collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
    myCounter,
)
```

## golusoris usage

- `k8s/metrics/prom/` — `/metrics` handler + per-check-status gauges provided via fx.

## Links

- Changelog: https://github.com/prometheus/client_golang/blob/main/CHANGELOG.md
- Best practices: https://prometheus.io/docs/practices/naming/
