// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grafana_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/observability/grafana"
	"github.com/golusoris/golusoris/observability/metricdef"
)

var (
	jobs = metricdef.Def{Name: "app_jobs_total", Help: "Finished jobs", Kind: metricdef.KindCounter, Labels: []string{"tenant", "outcome"}}
	lat  = metricdef.Def{
		Name: "app_job_duration_seconds", Help: "Job latency", Unit: "seconds",
		Kind: metricdef.KindHistogram, Labels: []string{"tenant", "backend"},
	}
	depth = metricdef.Def{Name: "app_queue_bytes", Help: "Queued bytes", Unit: "bytes", Kind: metricdef.KindGauge, Labels: []string{"tenant"}}
)

func TestExprs(t *testing.T) {
	t.Parallel()
	byTenant := grafana.Query{Def: jobs, By: []string{"tenant"}, Filters: []string{"tenant"}}
	cases := map[string]func() (string, error){
		`sum by (tenant) (rate(app_jobs_total{tenant=~"$tenant"}[$__rate_interval]))`: func() (string, error) {
			return grafana.RateExpr(byTenant)
		},
		`sum(rate(app_job_duration_seconds_count[$__rate_interval]))`: func() (string, error) {
			return grafana.RateExpr(grafana.Query{Def: lat})
		},
		`sum by (tenant) (increase(app_jobs_total{tenant=~"$tenant"}[$__range]))`: func() (string, error) {
			return grafana.IncreaseExpr(byTenant)
		},
		`histogram_quantile(0.99, sum by (le, backend) (rate(app_job_duration_seconds_bucket{tenant=~"$tenant", backend=~"gpu|cpu"}[$__rate_interval])))`: func() (string, error) {
			return grafana.QuantileExpr(grafana.Query{Def: lat, By: []string{"backend"}, Filters: []string{"tenant"}, Matchers: map[string]string{"backend": "gpu|cpu"}}, 0.99)
		},
		`sum(app_queue_bytes{tenant=~"say \"hi\""})`: func() (string, error) {
			return grafana.ValueExpr(grafana.Query{Def: depth, Matchers: map[string]string{"tenant": `say "hi"`}})
		},
		`(sum(rate(app_jobs_total{outcome=~"failed"}[$__rate_interval]))) / (sum(rate(app_jobs_total[$__rate_interval])))`: func() (string, error) {
			return grafana.RatioExpr(grafana.Query{Def: jobs, Matchers: map[string]string{"outcome": "failed"}}, grafana.Query{Def: jobs})
		},
	}
	for want, build := range cases {
		got, err := build()
		if err != nil || got != want {
			t.Errorf("got %q, %v\nwant %q", got, err, want)
		}
	}
}

func TestExprsRejectUnknownLabelsAndKinds(t *testing.T) {
	t.Parallel()
	errCases := map[string]func() (string, error){
		"by unknown label":     func() (string, error) { return grafana.RateExpr(grafana.Query{Def: jobs, By: []string{"status"}}) },
		"filter unknown label": func() (string, error) { return grafana.RateExpr(grafana.Query{Def: jobs, Filters: []string{"node"}}) },
		"matcher unknown": func() (string, error) {
			return grafana.ValueExpr(grafana.Query{Def: depth, Matchers: map[string]string{"x": "y"}})
		},
		"rate of gauge":       func() (string, error) { return grafana.RateExpr(grafana.Query{Def: depth}) },
		"quantile of counter": func() (string, error) { return grafana.QuantileExpr(grafana.Query{Def: jobs}, 0.5) },
		"quantile 1":          func() (string, error) { return grafana.QuantileExpr(grafana.Query{Def: lat}, 1) },
		"quantile 0":          func() (string, error) { return grafana.QuantileExpr(grafana.Query{Def: lat}, 0) },
		"value of histogram":  func() (string, error) { return grafana.ValueExpr(grafana.Query{Def: lat}) },
		"increase of gauge":   func() (string, error) { return grafana.IncreaseExpr(grafana.Query{Def: depth}) },
		"ratio groups differ": func() (string, error) {
			return grafana.RatioExpr(grafana.Query{Def: jobs, By: []string{"tenant"}}, grafana.Query{Def: jobs})
		},
		"ratio bad numerator":   func() (string, error) { return grafana.RatioExpr(grafana.Query{Def: depth}, grafana.Query{Def: jobs}) },
		"ratio bad denominator": func() (string, error) { return grafana.RatioExpr(grafana.Query{Def: jobs}, grafana.Query{Def: depth}) },
	}
	for name, build := range errCases {
		if _, err := build(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestUnits(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"seconds": "s", "bytes": "bytes", "ratio": "percentunit", "": "short", "frames": "short"} {
		if got := grafana.ValueUnit(in); got != want {
			t.Errorf("ValueUnit(%q) = %q, want %q", in, got, want)
		}
	}
	if grafana.RateUnit("bytes") != "Bps" || grafana.RateUnit("") != "ops" {
		t.Error("RateUnit mismatch")
	}
}

// renderedDashboard is the subset of dashboard JSON the tests inspect.
type renderedDashboard struct {
	UID        string `json:"uid"`
	Templating struct {
		List []struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Query any    `json:"query"`
		} `json:"list"`
	} `json:"templating"`
	Annotations struct {
		List []struct {
			Name string `json:"name"`
			Expr string `json:"expr"`
		} `json:"list"`
	} `json:"annotations"`
	Panels []struct {
		Title       string `json:"title"`
		Type        string `json:"type"`
		FieldConfig struct {
			Defaults struct {
				Unit       string `json:"unit"`
				Thresholds struct {
					Steps []struct {
						Value *float64 `json:"value"`
						Color string   `json:"color"`
					} `json:"steps"`
				} `json:"thresholds"`
			} `json:"defaults"`
		} `json:"fieldConfig"` //nolint:tagliatelle // Grafana JSON model is camelCase.
		Links []struct {
			URL string `json:"url"`
		} `json:"links"`
		Options struct {
			Legend struct {
				ShowLegend bool `json:"showLegend"` //nolint:tagliatelle // Grafana JSON model is camelCase.
			} `json:"legend"`
			Tooltip struct {
				Mode string `json:"mode"`
			} `json:"tooltip"`
		} `json:"options"`
		Targets []struct {
			Expr       string `json:"expr"`
			Exemplar   bool   `json:"exemplar"`
			Datasource struct {
				UID string `json:"uid"`
			} `json:"datasource"`
		} `json:"targets"`
	} `json:"panels"`
}

func checkAll(t *testing.T, errs ...error) {
	t.Helper()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRenderDashboardFromDefs(t *testing.T) {
	t.Parallel()
	q := grafana.Query{Def: lat, By: []string{"backend"}, Filters: []string{"tenant"}}
	tenantVar, errVar := grafana.LabelVariable(lat, "tenant")
	quantiles, errQ := grafana.QuantilePanel(q, grafana.PanelOptions{
		Thresholds: []grafana.Threshold{{Value: 1, Color: "red"}},
		Links:      []grafana.Link{{Title: "Traces", URL: "/explore"}},
	}, 0.5, 0.99)
	jobRate, errJR := grafana.RatePanel(grafana.Query{Def: jobs, By: []string{"outcome"}}, grafana.PanelOptions{Title: "Jobs/s"})
	obsRate, errOR := grafana.RatePanel(q, grafana.PanelOptions{})
	bytesNow, errV := grafana.ValuePanel(grafana.Query{Def: depth}, grafana.PanelOptions{})
	failRatio, errR := grafana.RatioPanel(grafana.Query{Def: jobs, Matchers: map[string]string{"outcome": "failed"}}, grafana.Query{Def: jobs}, grafana.PanelOptions{Title: "Failure ratio"})
	bytesStat, errS1 := grafana.StatPanel(grafana.Query{Def: depth}, grafana.PanelOptions{
		Thresholds: []grafana.Threshold{{Value: 1e6, Color: "orange"}},
		Links:      []grafana.Link{{Title: "x", URL: "/y"}},
	})
	jobStat, errS2 := grafana.StatPanel(grafana.Query{Def: jobs}, grafana.PanelOptions{Unit: "none", Title: "Jobs"})
	checkAll(t, errVar, errQ, errJR, errOR, errV, errR, errS1, errS2)

	b := grafana.NewDashboard("app-overview", "App overview", "generated").
		WithVariable(tenantVar).
		Annotation(grafana.Annotation("Deploys", `changes(app_build_info[5m]) > 0`, "blue")).
		Links(grafana.Links(grafana.Link{Title: "Nodes", URL: grafana.DashboardURL("app-nodes")})).
		WithPanel(quantiles).
		WithPanel(jobRate).
		WithPanel(obsRate).
		WithPanel(bytesNow).
		WithPanel(failRatio).
		WithPanel(bytesStat).
		WithPanel(jobStat)

	raw, err := grafana.Render(b)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var d renderedDashboard
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("rendered JSON: %v", err)
	}
	if d.UID != "app-overview" || len(d.Panels) != 7 {
		t.Fatalf("uid %q, %d panels", d.UID, len(d.Panels))
	}
	if len(d.Templating.List) != 2 || d.Templating.List[0].Name != "datasource" || d.Templating.List[1].Query != "label_values(app_job_duration_seconds_bucket, tenant)" {
		t.Errorf("templating = %+v", d.Templating.List)
	}
	latency := d.Panels[0]
	if latency.Type != "timeseries" || latency.FieldConfig.Defaults.Unit != "s" || len(latency.Targets) != 2 || !latency.Targets[1].Exemplar {
		t.Errorf("latency panel = %+v", latency)
	}
	if !latency.Options.Legend.ShowLegend || latency.Options.Tooltip.Mode != "multi" {
		t.Errorf("latency legend/tooltip = %+v", latency.Options)
	}
	if latency.Targets[0].Datasource.UID != "${datasource}" || !strings.HasPrefix(latency.Targets[1].Expr, "histogram_quantile(0.99, ") {
		t.Errorf("latency targets = %+v", latency.Targets)
	}
	if steps := latency.FieldConfig.Defaults.Thresholds.Steps; len(steps) != 2 || steps[0].Value != nil || *steps[1].Value != 1 {
		t.Errorf("thresholds = %+v", steps)
	}
	if len(latency.Links) != 1 || latency.Links[0].URL != "/explore" {
		t.Errorf("links = %+v", latency.Links)
	}
	if d.Panels[3].FieldConfig.Defaults.Unit != "bytes" || d.Panels[4].FieldConfig.Defaults.Unit != "percentunit" || d.Panels[5].Type != "stat" {
		t.Errorf("unit/type mapping wrong: %+v", d.Panels[3:6])
	}
	if len(d.Annotations.List) == 0 || d.Annotations.List[len(d.Annotations.List)-1].Expr != `changes(app_build_info[5m]) > 0` {
		t.Errorf("annotations = %+v", d.Annotations.List)
	}
	if !strings.HasSuffix(string(raw), "}\n") {
		t.Error("render output lacks trailing newline")
	}
}

func TestPanelHelpersRejectBadInput(t *testing.T) {
	t.Parallel()
	if _, err := grafana.LabelVariable(lat, "node"); err == nil {
		t.Error("variable over unknown label")
	}
	if _, err := grafana.QuantilePanel(grafana.Query{Def: lat}, grafana.PanelOptions{}); err == nil {
		t.Error("quantile panel without quantiles")
	}
	if _, err := grafana.QuantilePanel(grafana.Query{Def: lat}, grafana.PanelOptions{}, 2); err == nil {
		t.Error("quantile 2 accepted")
	}
	if _, err := grafana.RatePanel(grafana.Query{Def: depth}, grafana.PanelOptions{}); err == nil {
		t.Error("rate panel over gauge")
	}
	if _, err := grafana.ValuePanel(grafana.Query{Def: jobs}, grafana.PanelOptions{}); err == nil {
		t.Error("value panel over counter")
	}
	if _, err := grafana.RatioPanel(grafana.Query{Def: depth}, grafana.Query{Def: jobs}, grafana.PanelOptions{}); err == nil {
		t.Error("ratio panel over gauge")
	}
	if _, err := grafana.StatPanel(grafana.Query{Def: lat}, grafana.PanelOptions{}); err == nil {
		t.Error("stat panel over histogram")
	}
}

func TestRenderRejectsNilAndInvalidBuilders(t *testing.T) {
	t.Parallel()
	if _, err := grafana.Render(nil); err == nil {
		t.Error("nil builder rendered")
	}
	broken := grafana.NewDashboard("x", "x").RecordError("panels", errBroken{})
	if _, err := grafana.Render(broken); err == nil {
		t.Error("builder with recorded error rendered")
	}
}

type errBroken struct{}

func (errBroken) Error() string { return "broken" }
