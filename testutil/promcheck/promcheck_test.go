// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package promcheck_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/golusoris/golusoris/observability/metricdef"
	"github.com/golusoris/golusoris/testutil/promcheck"
)

func testCatalog(t *testing.T) *metricdef.Catalog {
	t.Helper()
	cat, err := metricdef.NewCatalog(
		metricdef.Def{Name: "app_jobs_total", Help: "jobs", Kind: metricdef.KindCounter, Labels: []string{"tenant", "outcome"}},
		metricdef.Def{Name: "app_job_duration_seconds", Help: "latency", Unit: "seconds", Kind: metricdef.KindHistogram, Labels: []string{"tenant"}},
		metricdef.Def{Name: "app_queue_depth", Help: "depth", Kind: metricdef.KindGauge, Labels: []string{"queue"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func exprs(queries ...string) []promcheck.Expr {
	out := make([]promcheck.Expr, 0, len(queries))
	for i, q := range queries {
		out = append(out, promcheck.Expr{Source: fmt.Sprintf("q%d", i), Query: q})
	}
	return out
}

func TestCheckAcceptsEmittedMetricsAndLabels(t *testing.T) {
	t.Parallel()
	all := exprs(
		`sum by (tenant) (rate(app_jobs_total{tenant=~"$tenant", outcome="ok"}[$__rate_interval]))`,
		`histogram_quantile(0.99, sum by (le, tenant) (rate(app_job_duration_seconds_bucket[5m])))`,
		`sum by (team) (label_replace(app_jobs_total, "team", "$1", "tenant", "(.*)"))`,
		`count({__name__=~"app_jobs.*"})`,
		`up{job="app", namespace="prod"} == 0`,
		`topk($n, sum by (queue) (app_queue_depth))`,
		`max_over_time(app_queue_depth[${window}])`,
		`sum without (queue) (app_queue_depth)`,
		`avg_over_time(job:app_jobs:rate5m{anything="x"}[1h])`,
		`sum(increase(app_jobs_total[$__range])) / $__range_s`,
		`ALERTS{alertname="X"}`,
		`foreign_metric{x="y"}`,
	)
	all = append(all,
		promcheck.Expr{Source: "rec", Query: `sum by (job) (rate(app_jobs_total[5m]))`, Record: "job:app_jobs:rate5m"},
		promcheck.Expr{Source: "alert", Query: `app_queue_depth > 10`, TemplateLabels: []string{"queue", "severity"}, StaticLabels: []string{"severity"}},
		promcheck.Expr{Source: "alert2", Query: `sum by (tenant) (rate(app_jobs_total[5m])) > 1`, TemplateLabels: []string{"tenant"}},
	)
	got := promcheck.Check(all, testCatalog(t), promcheck.WithTargetLabels("namespace"), promcheck.WithOpenSeries("foreign_metric"))
	for _, f := range got {
		t.Errorf("unexpected finding: %s", f)
	}
}

func TestCheckReportsPlantedDefects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		expr promcheck.Expr
		want string
	}{
		{promcheck.Expr{Query: `rate(app_jobs_finished_total[5m])`}, `metric "app_jobs_finished_total" is not emitted`},
		{promcheck.Expr{Query: `rate(app_jobs_total{status=~"5.."}[5m])`}, `label "status" is not on app_jobs_total`},
		{promcheck.Expr{Query: `sum by (status) (rate(app_jobs_total[5m]))`}, `label "status" is on none of the aggregated series`},
		{promcheck.Expr{Query: `sum by (le) (rate(app_job_duration_seconds_count[5m]))`}, `label "le" is on none`},
		{promcheck.Expr{Query: `app_job_duration_seconds{tenant="a"}`}, `metric "app_job_duration_seconds" is not emitted`},
		{promcheck.Expr{Query: `sum(rate(app_jobs_total[5m])) >`}, "does not parse"},
		{promcheck.Expr{Query: `{tenant="a"}`}, "selector has no metric name"},
		{promcheck.Expr{Query: `{__name__=~"nothing_.*"}`}, "no known series matches"},
		{promcheck.Expr{Query: `{__name__=~"app_.*", status="x"}`}, ""},
		{promcheck.Expr{Query: `app_queue_depth == 0`, TemplateLabels: []string{"check"}}, "template reads $labels.check"},
		{promcheck.Expr{Query: `sum(app_queue_depth) > 1`, TemplateLabels: []string{"queue"}}, "template reads $labels.queue"},
		{promcheck.Expr{Query: `(sum by (queue) (app_queue_depth)) > 1`, TemplateLabels: []string{"tenant"}}, "template reads $labels.tenant"},
	}
	cat := testCatalog(t)
	for _, tc := range cases {
		tc.expr.Source = "planted"
		got := promcheck.Check([]promcheck.Expr{tc.expr}, cat)
		if tc.want == "" {
			if len(got) != 0 {
				t.Errorf("%s: regex-named selectors accept any label, got %v", tc.expr.Query, got)
			}
			continue
		}
		if len(got) == 0 || !strings.Contains(got[0].Problem, tc.want) {
			t.Errorf("%s: findings %v, want one mentioning %q", tc.expr.Query, got, tc.want)
		}
	}
}

// fakeTB records reports so the planted defect proves AssertKnownMetrics fails.
type fakeTB struct{ errs []string }

func (f *fakeTB) Helper() {}
func (f *fakeTB) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

func TestAssertKnownMetricsFailsOnUnknownMetric(t *testing.T) {
	t.Parallel()
	cat := testCatalog(t)
	bad := &fakeTB{}
	promcheck.AssertKnownMetrics(bad, exprs(`vmafx_controller_jobs_queued`, `app_queue_depth`), cat)
	if len(bad.errs) != 1 || !strings.Contains(bad.errs[0], "vmafx_controller_jobs_queued") {
		t.Fatalf("errors = %q, want exactly the undefined metric", bad.errs)
	}
	good := &fakeTB{}
	promcheck.AssertKnownMetrics(good, exprs(`app_queue_depth`), cat)
	if len(good.errs) != 0 {
		t.Fatalf("clean expressions reported %q", good.errs)
	}
	promcheck.AssertKnownMetrics(t, exprs(`sum by (queue) (app_queue_depth)`), cat)
}

func TestCatalogFromGatherer(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	jobs := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "app_jobs_total", Help: "x"}, []string{"tenant"})
	lat := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "app_latency_seconds", Help: "x"}, []string{"route"})
	sum := prometheus.NewSummary(prometheus.SummaryOpts{Name: "app_pause_seconds", Help: "x", Objectives: map[float64]float64{0.5: 0.05}})
	untyped := prometheus.NewUntypedFunc(prometheus.UntypedOpts{Name: "app_legacy", Help: "x"}, func() float64 { return 1 })
	reg.MustRegister(jobs, lat, sum, untyped)
	jobs.WithLabelValues("acme").Inc()
	lat.WithLabelValues("/x").Observe(1)
	sum.Observe(1)

	cat, err := promcheck.CatalogFromGatherer(reg)
	if err != nil {
		t.Fatalf("CatalogFromGatherer: %v", err)
	}
	checks := map[string][]string{
		"app_jobs_total":             {"tenant"},
		"app_latency_seconds_bucket": {"route", "le"},
		"app_pause_seconds":          {"quantile"},
		"app_legacy":                 nil,
	}
	for series, want := range checks {
		d, ok := cat.LookupSeries(series)
		if !ok || !slices.Equal(d.SeriesLabels(series), want) {
			t.Errorf("%s: %v %v, want labels %v", series, ok, d.SeriesLabels(series), want)
		}
	}
	if d, _ := cat.Lookup("app_legacy"); d.Kind != metricdef.KindGauge || !d.External {
		t.Errorf("untyped def = %+v, want External gauge", d)
	}
}

func TestCatalogFromGathererErrors(t *testing.T) {
	t.Parallel()
	if _, err := promcheck.CatalogFromGatherer(nil); err == nil {
		t.Error("nil gatherer accepted")
	}
	failing := prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) { return nil, errors.New("scrape failed") })
	if _, err := promcheck.CatalogFromGatherer(failing); err == nil {
		t.Error("gather error swallowed")
	}
	bogus := prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
		name, typ := "x", dto.MetricType(42)
		return []*dto.MetricFamily{{Name: &name, Type: &typ}}, nil
	})
	if _, err := promcheck.CatalogFromGatherer(bogus); err == nil {
		t.Error("unknown metric type accepted")
	}
	dupName := "dup_total"
	counter := dto.MetricType_COUNTER
	dup := prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
		return []*dto.MetricFamily{{Name: &dupName, Type: &counter}, {Name: &dupName, Type: &counter}}, nil
	})
	if _, err := promcheck.CatalogFromGatherer(dup); err == nil {
		t.Error("duplicate family accepted")
	}
}
