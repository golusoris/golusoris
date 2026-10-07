// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/golusoris/golusoris/httpx/middleware"
)

// deploy/observability/prometheus-rules.yaml filters on the Prometheus
// translation of these names; an otelhttp rename must fail here first.
const (
	serverDurationMetric = "http.server.request.duration"
	statusCodeAttribute  = "http.response.status_code"
)

func TestOTelEmitsStatusCodeAttributeRulesDependOn(t *testing.T) { //nolint:paralleltest // Swaps the global meter provider.
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown meter provider: %v", err)
		}
	})

	handler := middleware.OTel("rules", nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &collected); err != nil {
		t.Fatalf("collect: %v", err)
	}
	attrs := durationAttributes(t, collected)
	status, ok := attrs.Value(attribute.Key(statusCodeAttribute))
	if !ok || status.AsInt64() != http.StatusServiceUnavailable {
		t.Fatalf("%s = %v (present %t), want %d", statusCodeAttribute, status.AsInterface(), ok, http.StatusServiceUnavailable)
	}
	if _, legacy := attrs.Value("status"); legacy {
		t.Fatal(`legacy "status" attribute emitted; the shipped rules no longer filter on it`)
	}
}

func durationAttributes(t *testing.T, collected metricdata.ResourceMetrics) attribute.Set {
	t.Helper()
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != serverDurationMetric {
				continue
			}
			if m.Unit != "s" {
				t.Fatalf("%s unit = %q, want s (rules query the _seconds suffix)", m.Name, m.Unit)
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok || len(hist.DataPoints) != 1 {
				t.Fatalf("%s data = %T with unexpected points", m.Name, m.Data)
			}
			return hist.DataPoints[0].Attributes
		}
	}
	t.Fatalf("metric %s not emitted", serverDurationMetric)
	return attribute.Set{}
}
