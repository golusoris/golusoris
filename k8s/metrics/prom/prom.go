// Package prom mounts a Prometheus /metrics endpoint and exposes the check
// registry's per-check status as Gauges so Prometheus alerting can fire on
// individual dep failures (not just overall).
//
// Default registry: prometheus.DefaultRegisterer (Go runtime + process
// collectors are auto-registered when the prometheus/client_golang package
// is imported).
//
// Apps wire collectors via prometheus.MustRegister(...) at startup. The
// /metrics handler picks them all up.
package prom

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/golusoris/golusoris/observability/statuspage"
)

// Handler returns the standard Prometheus /metrics handler reading from
// prometheus.DefaultGatherer.
func Handler() http.Handler { return promhttp.Handler() }

// Mount attaches /metrics to r and (when reg != nil) registers a status
// gauge per check on the default Prometheus registry.
func Mount(r chi.Router, reg *statuspage.Registry) {
	if reg != nil {
		registerCheckStatus(reg)
	}
	r.Handle("/metrics", Handler())
}

// CheckStatusGauge is the gauge family exposing per-check status as 0/1.
//
//	app_check_status{name="db"} 1   # up
//	app_check_status{name="db"} 0   # down
var CheckStatusGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "app_check_status",
		Help: "1 = check up, 0 = check down (last evaluation).",
	},
	[]string{"name"},
)

// CheckLatencySeconds is the gauge family exposing per-check evaluation
// latency in seconds.
var CheckLatencySeconds = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "app_check_latency_seconds",
		Help: "Latency of the last check evaluation, in seconds.",
	},
	[]string{"name"},
)

// registerCheckStatus wires the gauges onto the default registry + installs
// a Run hook that snapshots results into the gauges. The gauge values
// refresh whenever any /livez /readyz /startupz /status request runs the
// registry — Prometheus then scrapes the latest snapshot.
//
// Idempotent: panics from MustRegister are recovered (multiple Mount calls
// are tolerated, e.g. tests).
func registerCheckStatus(reg *statuspage.Registry) {
	defer func() { _ = recover() }() // tolerate "already registered" on repeat Mount
	prometheus.MustRegister(CheckStatusGauge, CheckLatencySeconds)

	reg.OnRun(func(_ context.Context, results []statuspage.Result) {
		for _, res := range results {
			val := 0.0
			if res.Status == statuspage.StatusUp {
				val = 1.0
			}
			CheckStatusGauge.WithLabelValues(res.Name).Set(val)
			if d, err := time.ParseDuration(res.Latency); err == nil {
				CheckLatencySeconds.WithLabelValues(res.Name).Set(d.Seconds())
			}
		}
	})
}
