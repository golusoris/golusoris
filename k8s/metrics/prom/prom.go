// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	"errors"
	"fmt"
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
// gauge per check on the default Prometheus registry. Repeat calls are
// tolerated (already-registered gauges are reused); any other registration
// failure is returned.
func Mount(r chi.Router, reg *statuspage.Registry) error {
	if reg != nil {
		if err := registerCheckStatusOn(prometheus.DefaultRegisterer, reg); err != nil {
			return err
		}
	}
	r.Handle("/metrics", Handler())
	return nil
}

// HandlerFor returns a /metrics handler that serves a specific
// [*prometheus.Registry] instead of the global default — for apps that keep
// their business metrics on their own registry.
func HandlerFor(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

// MountFor attaches /metrics (serving reg) to a net/http mux and, when checks
// != nil, registers the per-check status gauges on reg. No chi dependency — for
// apps that don't run chi. Repeat calls are tolerated; any other registration
// failure is returned.
func MountFor(mux *http.ServeMux, reg *prometheus.Registry, checks *statuspage.Registry) error {
	if checks != nil {
		if err := registerCheckStatusOn(reg, checks); err != nil {
			return err
		}
	}
	mux.Handle("/metrics", HandlerFor(reg))
	return nil
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

// registerCheckStatusOn wires the gauges onto reg + installs a Run hook that
// snapshots results into the gauges. The gauge values refresh whenever any
// /livez /readyz /startupz /status request runs the registry — Prometheus then
// scrapes the latest snapshot.
//
// Idempotent: an AlreadyRegisteredError is accepted (multiple Mount calls are
// tolerated, e.g. tests); any other registration error is returned.
func registerCheckStatusOn(reg prometheus.Registerer, checks *statuspage.Registry) error {
	for _, c := range []prometheus.Collector{CheckStatusGauge, CheckLatencySeconds} {
		if err := registerTolerant(reg, c); err != nil {
			return err
		}
	}
	snapshotChecks(checks)
	return nil
}

// registerTolerant registers c on reg, treating "already registered" as success.
func registerTolerant(reg prometheus.Registerer, c prometheus.Collector) error {
	err := reg.Register(c)
	var are prometheus.AlreadyRegisteredError
	if err == nil || errors.As(err, &are) {
		return nil
	}
	return fmt.Errorf("prom: register check gauge: %w", err)
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
