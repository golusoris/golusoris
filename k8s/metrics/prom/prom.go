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

// HandlerFor returns a /metrics handler that serves a specific
// [*prometheus.Registry] instead of the global default — for apps that keep
// their business metrics on their own registry.
func HandlerFor(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

// MountFor attaches /metrics (serving reg) to a net/http mux and, when checks
// != nil, registers the per-check status gauges on reg. No chi dependency — for
// apps that don't run chi.
func MountFor(mux *http.ServeMux, reg *prometheus.Registry, checks *statuspage.Registry) {
	if checks != nil {
		registerCheckStatusOn(reg, checks)
	}
	mux.Handle("/metrics", HandlerFor(reg))
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
func registerCheckStatus(checks *statuspage.Registry) {
	defer func() { _ = recover() }() // tolerate "already registered" on repeat Mount
	prometheus.MustRegister(CheckStatusGauge, CheckLatencySeconds)
	snapshotChecks(checks)
}

// registerCheckStatusOn is the custom-registry variant of registerCheckStatus.
func registerCheckStatusOn(reg *prometheus.Registry, checks *statuspage.Registry) {
	defer func() { _ = recover() }() // tolerate "already registered" on repeat MountFor
	reg.MustRegister(CheckStatusGauge, CheckLatencySeconds)
	snapshotChecks(checks)
}

// snapshotChecks installs the hook that mirrors each check's last result into
// the status/latency gauges (refreshed whenever the registry runs).
func snapshotChecks(checks *statuspage.Registry) {
	checks.OnRun(func(_ context.Context, results []statuspage.Result) {
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
