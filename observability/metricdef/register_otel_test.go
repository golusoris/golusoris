// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package metricdef_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/golusoris/golusoris/observability/metricdef"
	golusoris_otel "github.com/golusoris/golusoris/otel"
)

func prometheusMeter(t *testing.T) (*prometheus.Registry, metric.Meter) {
	t.Helper()
	for _, env := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_SDK_DISABLED"} {
		t.Setenv(env, "")
	}
	reg := prometheus.NewRegistry()
	opts := golusoris_otel.Options{
		Enabled: true,
		Service: golusoris_otel.ServiceOptions{Name: "metricdef-test"},
		Export:  golusoris_otel.ExportOptions{Prometheus: true},
	}
	providers, err := golusoris_otel.NewWithRegisterer(t.Context(), opts, reg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mustOK(t, providers.Shutdown(context.Background())) })
	if providers.Meter == nil {
		t.Fatal("OTel meter provider missing")
	}
	return reg, providers.Meter.Meter("metricdef-test")
}

func TestRegisterOTelRoundTrip(t *testing.T) { //nolint:paralleltest // Provider setup uses t.Setenv and OTel globals.
	reg, meter := prometheusMeter(t)
	defs := []metricdef.Def{
		jobsTotal, queueDepth, jobSeconds,
		{Name: "sent_bytes_total", Help: "Sent bytes.", Kind: metricdef.KindCounter, Unit: "bytes"},
		{Name: "cpu_seconds_total", Help: "CPU time.", Kind: metricdef.KindCounter, Unit: "seconds"},
		{Name: "ready_ratio", Help: "Ready fraction.", Kind: metricdef.KindGauge, Unit: "ratio"},
		{Name: "payload_bytes", Help: "Payload sizes.", Kind: metricdef.KindHistogram, Unit: "bytes"},
		{Name: "active_widgets", Help: "Active widgets.", Kind: metricdef.KindGauge, Unit: "widgets"},
		{Name: "queue_size", Help: "Queue sizes.", Kind: metricdef.KindHistogram},
	}
	h, err := metricdef.RegisterOTel(meter, defs...)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		recordOTelDef(t, h, d)
	}
	assertOTelRoundTrip(t, reg, defs)
	depth, err := h.Gauge(queueDepth.Name)
	mustOK(t, err, depth.Set(9, "value"), depth.Add(-2, "value"))
	if got := otelSeries(t, reg, queueDepth.Name)["queue=value"].GetGauge().GetValue(); got != 7 {
		t.Errorf("Set + Add = %v, want 7", got)
	}
	for _, name := range []string{"payload_bytes", "queue_size"} {
		buckets := otelSeries(t, reg, name)[""].GetHistogram().GetBucket()
		if len(buckets) != len(prometheus.DefBuckets) {
			t.Fatalf("%s buckets = %v, want default boundaries", name, buckets)
		}
		for i, bucket := range buckets {
			if bucket.GetUpperBound() != prometheus.DefBuckets[i] {
				t.Errorf("%s boundary %d = %v", name, i, bucket.GetUpperBound())
			}
		}
	}
	buckets := otelSeries(t, reg, jobSeconds.Name)["backend=value"].GetHistogram().GetBucket()
	if len(buckets) != len(jobSeconds.Buckets) || buckets[1].GetCumulativeCount() != 1 {
		t.Fatalf("explicit buckets = %v", buckets)
	}
	for i, bucket := range buckets {
		if bucket.GetUpperBound() != jobSeconds.Buckets[i] {
			t.Errorf("explicit boundary %d = %v", i, bucket.GetUpperBound())
		}
	}
}

func recordOTelDef(t *testing.T, h *metricdef.Handles, d metricdef.Def) {
	t.Helper()
	values := make([]string, len(d.Labels))
	for i := range values {
		values[i] = "value"
	}
	switch d.Kind {
	case metricdef.KindCounter:
		counter, err := h.Counter(d.Name)
		mustOK(t, err, counter.Inc(t.Context(), values...), counter.Add(t.Context(), 2, values...))
	case metricdef.KindGauge:
		gauge, err := h.Gauge(d.Name)
		mustOK(t, err, gauge.Add(5, values...))
	case metricdef.KindHistogram:
		histogram, err := h.Histogram(d.Name)
		mustOK(t, err, histogram.Observe(t.Context(), 0.5, values...))
	case metricdef.KindSummary:
		t.Fatal("unexpected summary")
	}
}

func assertOTelRoundTrip(t *testing.T, reg *prometheus.Registry, defs []metricdef.Def) {
	t.Helper()
	families, err := reg.Gather()
	mustOK(t, err)
	catalog, err := metricdef.NewCatalog(defs...)
	mustOK(t, err)
	seen := map[string]bool{}
	for _, family := range families {
		if family.GetName() == "target_info" || strings.HasPrefix(family.GetName(), "otel_scope_") {
			continue
		}
		def, ok := catalog.Lookup(family.GetName())
		if !ok || family.GetHelp() != def.Help {
			t.Errorf("unexpected family/help: %v", family)
		}
		assertOTelValues(t, family)
		var text bytes.Buffer
		_, err = expfmt.MetricFamilyToText(&text, family)
		mustOK(t, err)
		for _, line := range strings.Split(text.String(), "\n") {
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			name, _, _ := strings.Cut(strings.Fields(line)[0], "{")
			seen[name] = true
		}
	}
	got := make([]string, 0, len(seen))
	for name := range seen {
		got = append(got, name)
	}
	slices.Sort(got)
	if !slices.Equal(got, catalog.Series()) {
		t.Errorf("series = %v, want %v", got, catalog.Series())
	}
}

func assertOTelValues(t *testing.T, family *dto.MetricFamily) {
	t.Helper()
	if len(family.GetMetric()) != 1 {
		t.Fatalf("%s: want one attribute set, got %v", family.GetName(), family.GetMetric())
	}
	sample := family.GetMetric()[0]
	switch family.GetType() {
	case dto.MetricType_COUNTER:
		if sample.GetCounter().GetValue() != 3 {
			t.Errorf("%s: counter = %v, want 3", family.GetName(), sample.GetCounter())
		}
	case dto.MetricType_GAUGE:
		if sample.GetGauge().GetValue() != 5 {
			t.Errorf("%s: gauge = %v, want 5", family.GetName(), sample.GetGauge())
		}
	case dto.MetricType_HISTOGRAM:
		if sample.GetHistogram().GetSampleCount() != 1 || sample.GetHistogram().GetSampleSum() != 0.5 {
			t.Errorf("%s: histogram = %v, want count=1 sum=0.5", family.GetName(), sample.GetHistogram())
		}
	case dto.MetricType_SUMMARY, dto.MetricType_UNTYPED, dto.MetricType_GAUGE_HISTOGRAM:
		t.Errorf("unexpected type: %v", family.GetType())
	}
}

func otelSeries(t *testing.T, reg *prometheus.Registry, name string) map[string]*dto.Metric {
	t.Helper()
	out := map[string]*dto.Metric{}
	for _, sample := range series(t, reg, name) {
		labels := make([]string, 0, len(sample.GetLabel()))
		for _, label := range sample.GetLabel() {
			if !strings.HasPrefix(label.GetName(), "otel_scope_") {
				labels = append(labels, label.GetName()+"="+label.GetValue())
			}
		}
		out[strings.Join(labels, ",")] = sample
	}
	return out
}

func TestOTelLabelLimitsAndGaugeConcurrency(t *testing.T) { //nolint:paralleltest // Provider setup uses t.Setenv and OTel globals.
	reg, meter := prometheusMeter(t)
	gaugeDef := metricdef.Def{
		Name: "bounded_depth", Help: "Depth.", Kind: metricdef.KindGauge,
		Labels: []string{"tenant"}, Limits: map[string]metricdef.LabelLimit{"tenant": {MaxDistinct: 1}},
	}
	histDef := metricdef.Def{
		Name: "bounded_latency", Help: "Latency.", Kind: metricdef.KindHistogram,
		Labels: []string{"outcome"}, Limits: map[string]metricdef.LabelLimit{"outcome": {Allow: []string{"ok"}}},
	}
	h, err := metricdef.RegisterOTel(meter, jobsTotal, gaugeDef, histDef)
	mustOK(t, err)
	jobs, err := h.Counter(jobsTotal.Name)
	mustOK(t, err)
	values := []string{"t1", "unexpected"}
	mustOK(t, jobs.Inc(t.Context(), values...))
	if values[1] != "unexpected" {
		t.Fatal("guard mutated caller values")
	}
	for _, tenant := range []string{"t2", "t3", "t1"} {
		mustOK(t, jobs.Inc(t.Context(), tenant, "ok"))
	}
	got := otelSeries(t, reg, jobsTotal.Name)
	for _, key := range []string{"outcome=other,tenant=t1", "outcome=ok,tenant=t2", "outcome=ok,tenant=other", "outcome=ok,tenant=t1"} {
		if len(got) != 4 || got[key].GetCounter().GetValue() != 1 {
			t.Errorf("bounded counter series = %v", got)
		}
	}
	gauge, err := h.Gauge(gaugeDef.Name)
	mustOK(t, err, gauge.Set(10, "first"))
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 50 {
				if addErr := gauge.Add(1, "overflow"); addErr != nil {
					t.Error(addErr)
					return
				}
			}
		})
	}
	workers.Wait()
	mustOK(t, gauge.Add(-1, "different"), gauge.Add(-2, "first"))
	depth := otelSeries(t, reg, gaugeDef.Name)
	if len(depth) != 2 || depth["tenant=other"].GetGauge().GetValue() != 399 || depth["tenant=first"].GetGauge().GetValue() != 8 {
		t.Errorf("bounded concurrent gauge = %v", depth)
	}
	histogram, err := h.Histogram(histDef.Name)
	mustOK(t, err, histogram.Observe(t.Context(), 1, "unexpected"), histogram.Observe(t.Context(), 2, "also-unexpected"))
	latency := otelSeries(t, reg, histDef.Name)
	if len(latency) != 1 || latency["outcome=other"].GetHistogram().GetSampleCount() != 2 {
		t.Errorf("bounded histogram = %v", latency)
	}
}

func TestOTelInstrumentNameBoundary(t *testing.T) {
	t.Parallel()
	for _, kind := range []metricdef.Kind{metricdef.KindCounter, metricdef.KindGauge, metricdef.KindHistogram} {
		t.Run(kind.String(), func(t *testing.T) {
			t.Parallel()
			provider := sdkmetric.NewMeterProvider()
			t.Cleanup(func() { mustOK(t, provider.Shutdown(context.Background())) })
			meter := provider.Meter("test")
			for _, length := range []int{255, 256} {
				name := strings.Repeat("a", length)
				if kind == metricdef.KindCounter {
					name += "_total"
				}
				h, err := metricdef.RegisterOTel(meter, metricdef.Def{Name: name, Help: "Boundary.", Kind: kind})
				if length == 255 {
					if err != nil || h == nil {
						t.Fatalf("valid name: %v, %v", h, err)
					}
					continue
				}
				if h != nil || !errors.Is(err, sdkmetric.ErrInstrumentName) || !strings.Contains(err.Error(), name) {
					t.Errorf("invalid name: %v, %v", h, err)
				}
			}
		})
	}
}

func TestRegisterOTelRejectsInvalidMappings(t *testing.T) {
	t.Parallel()
	meter := noop.NewMeterProvider().Meter("test")
	for _, def := range []metricdef.Def{
		{Name: "requests_ratio_total", Unit: "ratio", Help: "x", Kind: metricdef.KindCounter},
		{Name: "latency_ratio", Unit: "ratio", Help: "x", Kind: metricdef.KindHistogram},
		{Name: "app_value_min", Unit: "min", Help: "x", Kind: metricdef.KindGauge},
		{Name: "app_value__widgets", Unit: "value__widgets", Help: "x", Kind: metricdef.KindGauge},
		{Name: "app__depth", Help: "x", Kind: metricdef.KindGauge},
		{Name: "app_total_jobs_total", Help: "x", Kind: metricdef.KindCounter},
		{Name: "app_bytes_sent_bytes_total", Unit: "bytes", Help: "x", Kind: metricdef.KindCounter},
	} {
		t.Run(def.Name, func(t *testing.T) {
			t.Parallel()
			h, err := metricdef.RegisterOTel(meter, def)
			if h != nil || err == nil || !strings.Contains(err.Error(), def.Name) || !strings.Contains(err.Error(), "round-trip") {
				t.Errorf("RegisterOTel = %v, %v", h, err)
			}
		})
	}
	if _, err := metricdef.RegisterOTel(nil, jobsTotal); err == nil {
		t.Error("nil meter accepted")
	}
	if _, err := metricdef.RegisterOTel(meter, jobsTotal, jobsTotal); err == nil {
		t.Error("duplicate definitions accepted")
	}
	if _, err := metricdef.RegisterOTel(meter, metricdef.Def{Name: "bad"}); err == nil {
		t.Error("invalid definition accepted")
	}
	if h, err := metricdef.RegisterOTel(meter); h == nil || err != nil {
		t.Errorf("empty registration = %v, %v", h, err)
	}
}

func TestCatalogRegisterOTelSkipsExternalAndRejectsMisuse(t *testing.T) {
	t.Parallel()
	external := metricdef.Def{Name: "external:summary", Kind: metricdef.KindSummary, External: true}
	catalog, err := metricdef.NewCatalog(jobsTotal, queueDepth, jobSeconds, external)
	mustOK(t, err)
	meter := noop.NewMeterProvider().Meter("test")
	h, err := catalog.RegisterOTel(meter)
	mustOK(t, err)
	if _, err = h.Gauge(external.Name); err == nil {
		t.Error("external got a handle")
	}
	if _, err = metricdef.RegisterOTel(meter, external); err == nil {
		t.Error("standalone registration accepted external")
	}
	jobs, err := h.Counter(jobsTotal.Name)
	mustOK(t, err)
	depth, err := h.Gauge(queueDepth.Name)
	mustOK(t, err)
	latency, err := h.Histogram(jobSeconds.Name)
	mustOK(t, err)
	for _, misuse := range []error{
		jobs.Inc(t.Context()), jobs.Add(t.Context(), -1, "a", "ok"), jobs.Add(t.Context(), math.NaN(), "a", "ok"),
		depth.Set(1), depth.Add(1, "a", "b"), latency.Observe(t.Context(), 1),
	} {
		if misuse == nil {
			t.Error("invalid measurement accepted")
		}
	}
	mustOK(t, jobs.Add(t.Context(), 0, "a", "ok"))
}

func TestOTelInstrumentMetadata(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { mustOK(t, provider.Shutdown(context.Background())) })
	def := metricdef.Def{Name: "elapsed_seconds_total", Help: "Elapsed time.", Unit: "seconds", Kind: metricdef.KindCounter}
	h, err := metricdef.RegisterOTel(provider.Meter("test"), def)
	mustOK(t, err)
	counter, err := h.Counter(def.Name)
	mustOK(t, err, counter.Inc(t.Context()))
	var data metricdata.ResourceMetrics
	mustOK(t, reader.Collect(t.Context(), &data))
	if len(data.ScopeMetrics) != 1 || len(data.ScopeMetrics[0].Metrics) != 1 {
		t.Fatalf("unexpected metrics: %v", data)
	}
	got := data.ScopeMetrics[0].Metrics[0]
	if got.Name != "elapsed" || got.Unit != "s" || got.Description != def.Help {
		t.Errorf("instrument = %v", got)
	}
}
