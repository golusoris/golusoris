// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package prom

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"
)

// Exemplar label names; Grafana's exemplar-to-trace link matches TraceIDLabel.
// They equal the OTel Prometheus exporter's keys, so hand-made and
// OTel-produced exemplars open traces the same way.
const (
	TraceIDLabel = "trace_id"
	SpanIDLabel  = "span_id"
)

// OpenMetricsHandler serves g with OpenMetrics content negotiation enabled —
// the only exposition format that carries exemplars. Scrapers that do not ask
// for OpenMetrics still get the classic text format. Note: OpenMetrics renders
// integer-looking `le`/`quantile` label values with a trailing ".0";
// Prometheus 3 normalises them, Prometheus 2 sees new series once on switch.
func OpenMetricsHandler(g prometheus.Gatherer) http.Handler {
	return promhttp.HandlerFor(g, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

// ExemplarLabels returns trace_id/span_id labels for the sampled span in ctx,
// or nil when ctx carries no sampled span (an unsampled trace has nothing to
// open).
func ExemplarLabels(ctx context.Context) prometheus.Labels {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() || !sc.IsSampled() {
		return nil
	}
	return prometheus.Labels{
		TraceIDLabel: sc.TraceID().String(),
		SpanIDLabel:  sc.SpanID().String(),
	}
}

// ObserveWithExemplar records v on obs and, when obs supports exemplars and
// ctx carries a sampled span, links the observation to that trace.
func ObserveWithExemplar(ctx context.Context, obs prometheus.Observer, v float64) {
	labels := ExemplarLabels(ctx)
	if eo, ok := obs.(prometheus.ExemplarObserver); ok && labels != nil {
		eo.ObserveWithExemplar(v, labels)
		return
	}
	obs.Observe(v)
}
