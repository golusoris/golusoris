// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package prom_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	"github.com/golusoris/golusoris/k8s/metrics/prom"
)

const openMetricsAccept = "application/openmetrics-text;version=1.0.0"

var (
	exTraceID = trace.TraceID{0x0c, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	exSpanID  = trace.SpanID{0x0d, 1, 2, 3, 4, 5, 6, 7}
)

func withSpan(ctx context.Context, flags trace.TraceFlags) context.Context {
	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: exTraceID, SpanID: exSpanID, TraceFlags: flags,
	}))
}

func scrape(t *testing.T, g prometheus.Gatherer, accept string) string {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	prom.OpenMetricsHandler(g).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	return rec.Body.String()
}

func newLatency(t *testing.T) (*prometheus.Registry, prometheus.Histogram) {
	t.Helper()
	reg := prometheus.NewRegistry()
	h := prometheus.NewHistogram(prometheus.HistogramOpts{Name: "job_duration_seconds", Help: "x", Buckets: []float64{1, 5}})
	reg.MustRegister(h)
	return reg, h
}

func TestObserveWithExemplarLinksSampledTrace(t *testing.T) {
	t.Parallel()
	reg, h := newLatency(t)
	prom.ObserveWithExemplar(withSpan(t.Context(), trace.FlagsSampled), h, 0.5)

	body := scrape(t, reg, openMetricsAccept)
	// Exemplar label order follows map iteration, so match each label separately.
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, `job_duration_seconds_bucket{le="1.0"} 1 # {`) {
			continue
		}
		for _, want := range []string{`trace_id="` + exTraceID.String() + `"`, `span_id="` + exSpanID.String() + `"`, `} 0.5`} {
			if !strings.Contains(line, want) {
				t.Fatalf("exemplar line %q lacks %s", line, want)
			}
		}
		return
	}
	t.Fatalf("OpenMetrics body has no exemplar on the 1s bucket:\n%s", body)
}

func TestObserveWithExemplarSkipsUnsampledAndMissingSpans(t *testing.T) {
	t.Parallel()
	reg, h := newLatency(t)
	prom.ObserveWithExemplar(withSpan(t.Context(), 0), h, 0.5)
	prom.ObserveWithExemplar(t.Context(), h, 2)

	body := scrape(t, reg, openMetricsAccept)
	if strings.Contains(body, "trace_id") {
		t.Fatalf("exemplar without a sampled span:\n%s", body)
	}
	if !strings.Contains(body, "job_duration_seconds_count 2") {
		t.Fatalf("observations lost:\n%s", body)
	}
}

func TestObserveWithExemplarFallsBackForPlainObserver(t *testing.T) {
	t.Parallel()
	var got []float64
	obs := prometheus.ObserverFunc(func(v float64) { got = append(got, v) })
	prom.ObserveWithExemplar(withSpan(t.Context(), trace.FlagsSampled), obs, 3)
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("plain observer saw %v, want [3]", got)
	}
}

func TestOpenMetricsHandlerKeepsClassicFormatForClassicScrapers(t *testing.T) {
	t.Parallel()
	reg, h := newLatency(t)
	prom.ObserveWithExemplar(withSpan(t.Context(), trace.FlagsSampled), h, 0.5)

	body := scrape(t, reg, "")
	if strings.Contains(body, "# EOF") || strings.Contains(body, "trace_id") {
		t.Fatalf("classic scrape got OpenMetrics output:\n%s", body)
	}
}

func TestExemplarLabels(t *testing.T) {
	t.Parallel()
	if got := prom.ExemplarLabels(t.Context()); got != nil {
		t.Fatalf("no span: got %v", got)
	}
	got := prom.ExemplarLabels(withSpan(t.Context(), trace.FlagsSampled))
	if got[prom.TraceIDLabel] != exTraceID.String() || got[prom.SpanIDLabel] != exSpanID.String() {
		t.Fatalf("labels = %v", got)
	}
}
