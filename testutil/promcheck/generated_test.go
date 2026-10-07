// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package promcheck_test

import (
	"strings"
	"testing"

	"github.com/golusoris/golusoris/observability/grafana"
	"github.com/golusoris/golusoris/observability/metricdef"
	"github.com/golusoris/golusoris/observability/rules"
	"github.com/golusoris/golusoris/testutil/promcheck"
)

const genRunbook = "https://runbooks.example.org/jobs"

var (
	genJobs    = metricdef.Def{Name: "app_jobs_total", Help: "Jobs", Kind: metricdef.KindCounter, Labels: []string{"tenant", "outcome"}}
	genLatency = metricdef.Def{Name: "app_job_duration_seconds", Help: "Latency", Unit: "seconds", Kind: metricdef.KindHistogram, Labels: []string{"tenant"}}
	genDepth   = metricdef.Def{Name: "app_queue_depth", Help: "Depth", Kind: metricdef.KindGauge, Labels: []string{"tenant"}}
)

// generatedDashboard renders every observability/grafana helper from the
// catalog; extraAnnotation lets a test plant a hand-written query.
func generatedDashboard(t *testing.T, extraAnnotation string) []byte {
	t.Helper()
	q := grafana.Query{Def: genLatency, By: []string{"tenant"}, Filters: []string{"tenant"}}
	tenant, err1 := grafana.LabelVariable(genLatency, "tenant")
	p99, err2 := grafana.QuantilePanel(q, grafana.PanelOptions{}, 0.5, 0.99)
	rate, err3 := grafana.RatePanel(grafana.Query{Def: genJobs, By: []string{"outcome"}, Filters: []string{"tenant"}}, grafana.PanelOptions{})
	ratio, err4 := grafana.RatioPanel(grafana.Query{Def: genJobs, Matchers: map[string]string{"outcome": "failed"}}, grafana.Query{Def: genJobs}, grafana.PanelOptions{})
	depth, err5 := grafana.StatPanel(grafana.Query{Def: genDepth, Filters: []string{"tenant"}}, grafana.PanelOptions{})
	checkAll(t, err1, err2, err3, err4, err5)
	b := grafana.NewDashboard("gen", "Generated").WithVariable(tenant).
		WithPanel(p99).WithPanel(rate).WithPanel(ratio).WithPanel(depth)
	if extraAnnotation != "" {
		b = b.Annotation(grafana.Annotation("Planted", extraAnnotation, "red"))
	}
	raw, err := grafana.Render(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func checkAll(t *testing.T, errs ...error) {
	t.Helper()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func generatedRules(t *testing.T) []byte {
	t.Helper()
	slo, err := rules.SLO{
		Name: "jobs_latency", AlertName: "JobsLatency", Objective: 0.99, RunbookURL: genRunbook,
		SLI: rules.LatencySLI{Histogram: genLatency.Name, Threshold: 1},
	}.Group()
	if err != nil {
		t.Fatal(err)
	}
	queue := rules.Group{Name: "queue", Alerts: []rules.Alert{{
		Name: "QueueAging", Expr: `max by (tenant) (app_queue_depth) > 100`, Severity: rules.SeverityWarning,
		Summary: "Queue of {{ $labels.tenant }} is deep", RunbookURL: genRunbook,
	}}}
	raw, err := rules.RenderRuleFile(slo, queue)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGeneratedDashboardsAndRulesQueryOnlyCatalogMetrics(t *testing.T) {
	t.Parallel()
	cat, err := metricdef.NewCatalog(genJobs, genLatency, genDepth)
	if err != nil {
		t.Fatal(err)
	}
	dash, err := promcheck.DashboardExprs(generatedDashboard(t, ""), "generated.json")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := promcheck.RuleExprs(generatedRules(t), "generated-rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(dash) < 6 || len(rs) < 10 {
		t.Fatalf("too few expressions extracted: %d dashboard, %d rules", len(dash), len(rs))
	}
	promcheck.AssertKnownMetrics(t, append(dash, rs...), cat)
}

func TestGeneratedDashboardWithStaleQueryFails(t *testing.T) {
	t.Parallel()
	cat, err := metricdef.NewCatalog(genJobs, genLatency, genDepth)
	if err != nil {
		t.Fatal(err)
	}
	// The vmafx #2430 failure: a panel still queries a renamed metric.
	dash, err := promcheck.DashboardExprs(generatedDashboard(t, "vmafx_controller_jobs_queued > 0"), "generated.json")
	if err != nil {
		t.Fatal(err)
	}
	reporter := &fakeTB{}
	promcheck.AssertKnownMetrics(reporter, dash, cat)
	if len(reporter.errs) != 1 || !strings.Contains(reporter.errs[0], `annotation "Planted"`) {
		t.Fatalf("errors = %q, want one for the planted annotation", reporter.errs)
	}
}
