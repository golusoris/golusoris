// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package otel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	otelapi "go.opentelemetry.io/otel"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/log"
	"github.com/golusoris/golusoris/httpx/middleware"
	"github.com/golusoris/golusoris/k8s/metrics/prom"
	golusoris_otel "github.com/golusoris/golusoris/otel"
)

const sampledTraceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"

func prometheusOnly() golusoris_otel.Options {
	return golusoris_otel.Options{
		Enabled: true,
		Service: golusoris_otel.ServiceOptions{Name: "test"},
		Export:  golusoris_otel.ExportOptions{Prometheus: true},
	}
}

func clearOTLPEnv(t *testing.T) {
	t.Helper()
	for _, env := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_SDK_DISABLED"} {
		t.Setenv(env, "")
	}
}

func shutdownOnCleanup(t *testing.T, providers *golusoris_otel.Providers) {
	t.Helper()
	t.Cleanup(func() {
		if err := providers.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
}

func family(t *testing.T, g prometheus.Gatherer, name string) *dto.MetricFamily {
	t.Helper()
	families, err := g.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == name {
			return f
		}
	}
	return nil
}

func TestPrometheusExportServesOTelHTTPMetricsWithoutEndpoint(t *testing.T) { //nolint:paralleltest // clearOTLPEnv calls t.Setenv.
	clearOTLPEnv(t)
	reg := prometheus.NewRegistry()
	providers, err := golusoris_otel.NewWithRegisterer(t.Context(), prometheusOnly(), reg)
	if err != nil {
		t.Fatalf("NewWithRegisterer: %v", err)
	}
	shutdownOnCleanup(t, providers)
	if providers.Meter == nil || providers.Tracer != nil || providers.Logger != nil {
		t.Fatalf("want meter only, got tracer=%v meter=%v logger=%v", providers.Tracer != nil, providers.Meter != nil, providers.Logger != nil)
	}

	handler := middleware.OTel("prom", nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set("Traceparent", sampledTraceparent)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// The exact family and label deploy/observability/prometheus-rules.yaml queries.
	f := family(t, reg, "http_server_request_duration_seconds")
	if f == nil || f.GetType() != dto.MetricType_HISTOGRAM {
		t.Fatalf("http_server_request_duration_seconds histogram missing: %v", f)
	}
	if !hasLabel(f.GetMetric()[0], "http_response_status_code", "503") {
		t.Fatalf("labels = %v, want http_response_status_code=503", f.GetMetric()[0].GetLabel())
	}

	rec := httptest.NewRecorder()
	scrape := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	scrape.Header.Set("Accept", "application/openmetrics-text;version=1.0.0")
	prom.OpenMetricsHandler(reg).ServeHTTP(rec, scrape)
	if !strings.Contains(rec.Body.String(), `trace_id="0af7651916cd43dd8448eb211c80319c"`) {
		t.Fatalf("latency exemplar does not open the request trace:\n%s", rec.Body.String())
	}
}

func hasLabel(m *dto.Metric, name, value string) bool {
	for _, l := range m.GetLabel() {
		if l.GetName() == name && l.GetValue() == value {
			return true
		}
	}
	return false
}

func TestPrometheusExportOffWhenDisabled(t *testing.T) { //nolint:paralleltest // clearOTLPEnv calls t.Setenv.
	clearOTLPEnv(t)
	opts := prometheusOnly()
	opts.Enabled = false
	reg := prometheus.NewRegistry()
	providers, err := golusoris_otel.NewWithRegisterer(t.Context(), opts, reg)
	if err != nil {
		t.Fatalf("NewWithRegisterer: %v", err)
	}
	emptyProviders(t, providers)
	if families, gatherErr := reg.Gather(); gatherErr != nil || len(families) != 0 {
		t.Fatalf("disabled module registered metrics: %v %v", families, gatherErr)
	}
}

func TestPrometheusExportAlongsideOTLP(t *testing.T) { //nolint:paralleltest // clearOTLPEnv calls t.Setenv.
	clearOTLPEnv(t)
	opts := prometheusOnly()
	opts.Endpoint, opts.Insecure, opts.Export.Metrics = "127.0.0.1:1", true, true
	reg := prometheus.NewRegistry()
	providers, err := golusoris_otel.NewWithRegisterer(t.Context(), opts, reg)
	if err != nil {
		t.Fatalf("NewWithRegisterer: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		// The final OTLP flush cannot reach 127.0.0.1:1; only the pull side is under test.
		if shutdownErr := providers.Shutdown(ctx); shutdownErr != nil {
			t.Logf("shutdown against unreachable collector: %v", shutdownErr)
		}
	})

	counter, err := providers.Meter.Meter("test").Int64Counter("jobs")
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	counter.Add(t.Context(), 2)
	if family(t, reg, "jobs_total") == nil {
		t.Fatal("jobs_total missing from the Prometheus registry")
	}
}

func TestModuleUsesProvidedRegisterer(t *testing.T) {
	clearOTLPEnv(t)
	t.Setenv("APP_OTEL_SERVICE_NAME", "test")
	t.Setenv("APP_OTEL_EXPORT_PROMETHEUS", "true")
	prev := otelapi.GetMeterProvider()
	t.Cleanup(func() { otelapi.SetMeterProvider(prev) })

	reg := prometheus.NewRegistry()
	app := fxtest.New(t,
		fx.Provide(func() (*config.Config, error) { return config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."}) }),
		log.Module,
		golusoris_otel.Module,
		fx.Provide(func() prometheus.Registerer { return reg }),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	gauge, err := otelapi.Meter("test").Int64Gauge("queue_depth")
	if err != nil {
		t.Fatalf("gauge: %v", err)
	}
	gauge.Record(t.Context(), 7)
	if family(t, reg, "queue_depth") == nil {
		t.Fatal("module ignored the app's prometheus.Registerer")
	}
}
