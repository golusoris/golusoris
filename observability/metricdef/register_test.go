// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package metricdef_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/trace"

	"github.com/golusoris/golusoris/observability/metricdef"
)

func sampled(ctx context.Context) context.Context {
	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	}))
}

func register(t *testing.T, defs ...metricdef.Def) (*prometheus.Registry, *metricdef.Handles) {
	t.Helper()
	reg := prometheus.NewRegistry()
	h, err := metricdef.Register(reg, defs...)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return reg, h
}

// series returns "labels=value" for every sample of family name.
func series(t *testing.T, reg *prometheus.Registry, name string) map[string]*dto.Metric {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	out := map[string]*dto.Metric{}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			parts := make([]string, 0, len(m.GetLabel()))
			for _, l := range m.GetLabel() {
				parts = append(parts, l.GetName()+"="+l.GetValue())
			}
			out[strings.Join(parts, ",")] = m
		}
	}
	return out
}

func TestRegisterTypedHandlesRecord(t *testing.T) {
	t.Parallel()
	reg, h := register(t, jobsTotal, queueDepth, jobSeconds)
	ctx := t.Context()

	jobs, err := h.Counter(jobsTotal.Name)
	if err != nil {
		t.Fatal(err)
	}
	depth, err := h.Gauge(queueDepth.Name)
	if err != nil {
		t.Fatal(err)
	}
	latency, err := h.Histogram(jobSeconds.Name)
	if err != nil {
		t.Fatal(err)
	}
	mustOK(t, jobs.Inc(ctx, "acme", "ok"), jobs.Add(ctx, 2, "acme", "ok"))
	mustOK(t, depth.Set(5, "default"), depth.Add(-2, "default"))
	mustOK(t, latency.Observe(sampled(ctx), 0.5, "cpu"))

	if got := series(t, reg, "app_jobs_total")["outcome=ok,tenant=acme"].GetCounter().GetValue(); got != 3 {
		t.Errorf("jobs = %v, want 3", got)
	}
	if got := series(t, reg, "app_queue_depth")["queue=default"].GetGauge().GetValue(); got != 3 {
		t.Errorf("depth = %v, want 3", got)
	}
	buckets := series(t, reg, "app_job_duration_seconds")["backend=cpu"].GetHistogram().GetBucket()
	if buckets[1].GetExemplar() == nil {
		t.Error("histogram observation with a sampled span carries no exemplar")
	}
}

func mustOK(t *testing.T, errs ...error) {
	t.Helper()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCounterExemplarOnlyForSampledSpans(t *testing.T) {
	t.Parallel()
	reg, h := register(t, jobsTotal)
	jobs, err := h.Counter(jobsTotal.Name)
	if err != nil {
		t.Fatal(err)
	}
	mustOK(t, jobs.Inc(t.Context(), "a", "ok"), jobs.Inc(sampled(t.Context()), "b", "ok"))
	got := series(t, reg, "app_jobs_total")
	if got["outcome=ok,tenant=a"].GetCounter().GetExemplar() != nil {
		t.Error("exemplar without a span")
	}
	if got["outcome=ok,tenant=b"].GetCounter().GetExemplar() == nil {
		t.Error("no exemplar with a sampled span")
	}
}

func TestLabelLimitsFoldIntoOther(t *testing.T) {
	t.Parallel()
	reg, h := register(t, jobsTotal)
	jobs, err := h.Counter(jobsTotal.Name)
	if err != nil {
		t.Fatal(err)
	}
	values := []string{"t1", "unexpected"}
	mustOK(t, jobs.Inc(t.Context(), values...))
	if values[1] != "unexpected" {
		t.Error("guard mutated the caller's label slice")
	}
	for _, tenant := range []string{"t2", "t3", "t1"} {
		mustOK(t, jobs.Inc(t.Context(), tenant, "ok"))
	}
	want := map[string]float64{
		"outcome=other,tenant=t1": 1, // allowlist folds the outcome
		"outcome=ok,tenant=t2":    1,
		"outcome=ok,tenant=other": 1, // third distinct tenant exceeds MaxDistinct 2
		"outcome=ok,tenant=t1":    1, // already admitted values stay
	}
	got := series(t, reg, "app_jobs_total")
	if len(got) != len(want) {
		t.Fatalf("series = %v, want %v", keys(got), want)
	}
	for k, v := range want {
		if got[k].GetCounter().GetValue() != v {
			t.Errorf("%s = %v, want %v", k, got[k].GetCounter().GetValue(), v)
		}
	}
}

func keys(m map[string]*dto.Metric) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestDistinctLimitIsRaceFreeAndBounded(t *testing.T) {
	t.Parallel()
	reg, h := register(t, jobsTotal)
	jobs, err := h.Counter(jobsTotal.Name)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	const workers = 8
	for w := range workers {
		wg.Go(func() {
			for i := range 50 {
				if err := jobs.Inc(t.Context(), fmt.Sprintf("t%d-%d", w, i), "ok"); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if got := len(series(t, reg, "app_jobs_total")); got != 3 {
		t.Fatalf("%d tenant series, want 2 admitted + other", got)
	}
}

func TestHandlesRejectMisuse(t *testing.T) {
	t.Parallel()
	_, h := register(t, jobsTotal, queueDepth, jobSeconds)
	jobs, _ := h.Counter(jobsTotal.Name)
	depth, _ := h.Gauge(queueDepth.Name)
	latency, _ := h.Histogram(jobSeconds.Name)
	ctx := t.Context()
	for name, err := range map[string]error{
		"counter label count":   jobs.Inc(ctx, "only-one"),
		"negative counter":      jobs.Add(ctx, -1, "a", "ok"),
		"gauge label count":     depth.Set(1),
		"gauge add label count": depth.Add(1, "a", "b"),
		"histogram label count": latency.Observe(ctx, 1),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	for name, lookup := range map[string]func() error{
		"counter by gauge name": func() error { _, err := h.Counter(queueDepth.Name); return err },
		"gauge by missing name": func() error { _, err := h.Gauge("missing"); return err },
		"histogram by counter":  func() error { _, err := h.Histogram(jobsTotal.Name); return err },
	} {
		if lookup() == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestRegisterIsAllOrNothing(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{Name: jobSeconds.Name, Help: "taken"}))
	if _, err := metricdef.Register(reg, queueDepth, jobSeconds); err == nil {
		t.Fatal("Register over a taken name succeeded")
	}
	// queueDepth was rolled back, so registering it alone works.
	if _, err := metricdef.Register(reg, queueDepth); err != nil {
		t.Fatalf("rollback left %s registered: %v", queueDepth.Name, err)
	}
}

func TestRegisterRejectsBadInput(t *testing.T) {
	t.Parallel()
	external := metricdef.Def{Name: "http_server_request_duration_seconds", Help: "otelhttp", Unit: "seconds", Kind: metricdef.KindHistogram, External: true}
	if _, err := metricdef.Register(nil, queueDepth); err == nil {
		t.Error("nil registerer accepted")
	}
	if _, err := metricdef.Register(prometheus.NewRegistry(), external); err == nil {
		t.Error("external def registered")
	}
	if _, err := metricdef.Register(prometheus.NewRegistry(), metricdef.Def{Name: "x"}); err == nil {
		t.Error("invalid def registered")
	}
}

func TestCatalogRegisterSkipsExternal(t *testing.T) {
	t.Parallel()
	external := metricdef.Def{Name: "go_goroutines", Help: "Go collector", Kind: metricdef.KindGauge, External: true}
	cat, err := metricdef.NewCatalog(queueDepth, external)
	if err != nil {
		t.Fatal(err)
	}
	h, err := cat.Register(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("Catalog.Register: %v", err)
	}
	if _, err := h.Gauge(external.Name); err == nil {
		t.Error("external def got a handle")
	}
	if _, err := h.Gauge(queueDepth.Name); err != nil {
		t.Error(err)
	}
}
