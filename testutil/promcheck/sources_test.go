// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package promcheck_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/testutil/promcheck"
)

const dashboard = `{
  "dashboard": {
    "panels": [
      {"title": "Rate", "datasource": {"type": "prometheus", "uid": "${datasource}"},
       "targets": [{"refId": "A", "expr": "rate(app_jobs_total[5m])"}, {"refId": "B", "expr": ""}]},
      {"title": "Logs", "datasource": {"type": "loki", "uid": "loki"},
       "targets": [{"refId": "A", "expr": "{app=\"x\"} |= \"error\""}]},
      {"title": "Mixed", "datasource": {"type": "datasource", "uid": "-- Mixed --"},
       "targets": [{"refId": "A", "datasource": {"type": "prometheus"}, "expr": "app_queue_depth"},
                   {"refId": "B", "datasource": {"type": "tempo"}, "expr": "{}"}]},
      {"title": "Row", "type": "row", "panels": [
        {"title": "Nested", "datasource": "Prometheus", "targets": [{"refId": "A", "expr": "up"}]}
      ]}
    ],
    "templating": {"list": [
      {"name": "tenant", "type": "query", "query": "label_values(app_jobs_total{outcome=\"ok\"}, tenant)"},
      {"name": "queue", "type": "query", "query": {"query": "label_values(queue)", "refId": "Q"}},
      {"name": "top", "type": "query", "query": "query_result(topk(5, app_queue_depth))"},
      {"name": "names", "type": "query", "query": "label_names()"},
      {"name": "env", "type": "custom", "query": "prod,dev"},
      {"name": "lokivar", "type": "query", "datasource": {"type": "loki"}, "query": "label_values(app)"}
    ]},
    "annotations": {"list": [
      {"name": "Grafana", "datasource": {"type": "grafana", "uid": "-- Grafana --"}, "expr": ""},
      {"name": "Deploys", "datasource": {"type": "prometheus"}, "expr": "changes(app_build_info[5m]) > 0"},
      {"name": "Targeted", "datasource": {"type": "prometheus"}, "target": {"expr": "app_queue_depth > 100"}}
    ]}
  }
}`

func queries(es []promcheck.Expr) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Query)
	}
	return out
}

func TestDashboardExprs(t *testing.T) {
	t.Parallel()
	got, err := promcheck.DashboardExprs([]byte(dashboard), "dash.json")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"rate(app_jobs_total[5m])",
		"app_queue_depth",
		"up",
		`count by (tenant) (app_jobs_total{outcome="ok"})`,
		"topk(5, app_queue_depth)",
		"changes(app_build_info[5m]) > 0",
		"app_queue_depth > 100",
	}
	if !slices.Equal(queries(got), want) {
		t.Fatalf("queries =\n%q\nwant\n%q", queries(got), want)
	}
	if !strings.Contains(got[0].Source, `dash.json: panel "Rate" target A`) || !strings.Contains(got[3].Source, "variable $tenant") {
		t.Errorf("sources = %q / %q", got[0].Source, got[3].Source)
	}
}

func TestDashboardExprsRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	if _, err := promcheck.DashboardExprs([]byte(`{"panels": [`), "broken.json"); err == nil {
		t.Error("invalid JSON accepted")
	}
	got, err := promcheck.DashboardExprs([]byte(`{}`), "empty.json")
	if err != nil || len(got) != 0 {
		t.Errorf("empty dashboard = %v, %v", got, err)
	}
}

const ruleFiles = `# plain rule file
groups:
  - name: plain
    rules:
      - record: job:app_jobs:rate5m
        expr: sum by (job) (rate(app_jobs_total[5m]))
      - alert: Always
        expr: 1
---
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata: {name: app}
spec:
  groups:
    - name: cr
      rules:
        - alert: QueueDeep
          expr: app_queue_depth > 10
          labels: {severity: warning, owner: "{{ $labels.queue }}"}
          annotations:
            summary: "Queue {{ $labels.queue }} on {{$labels.instance}} is deep"
---
`

func TestRuleExprs(t *testing.T) {
	t.Parallel()
	got, err := promcheck.RuleExprs([]byte(ruleFiles), "rules.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d exprs: %+v", len(got), got)
	}
	if got[0].Record != "job:app_jobs:rate5m" || got[1].Query != "1" {
		t.Errorf("record/number expr = %+v / %+v", got[0], got[1])
	}
	alert := got[2]
	slices.Sort(alert.TemplateLabels)
	slices.Sort(alert.StaticLabels)
	if !slices.Equal(alert.TemplateLabels, []string{"instance", "queue", "queue"}) || !slices.Equal(alert.StaticLabels, []string{"owner", "severity"}) {
		t.Errorf("alert templates %v static %v", alert.TemplateLabels, alert.StaticLabels)
	}
	if !strings.Contains(alert.Source, `group "cr" rule 0 (QueueDeep)`) {
		t.Errorf("source = %s", alert.Source)
	}
}

func TestRuleExprsRejectsBadInput(t *testing.T) {
	t.Parallel()
	if _, err := promcheck.RuleExprs([]byte("groups: [\n"), "broken.yaml"); err == nil {
		t.Error("invalid YAML accepted")
	}
	if _, err := promcheck.RuleExprs([]byte("groups:\n- name: g\n  rules:\n  - alert: A\n    expr: {a: b}\n"), "map.yaml"); err == nil {
		t.Error("map expr accepted")
	}
	if _, err := promcheck.RuleExprs([]byte(strings.Repeat("a: 1\n---\n", 300)), "many.yaml"); err == nil {
		t.Error("document bound not enforced")
	}
}

func TestTemplateLabels(t *testing.T) {
	t.Parallel()
	got := promcheck.TemplateLabels(`{{ $labels.a }} {{$labels.b_c}} $value {{ $labels }}`)
	if !slices.Equal(got, []string{"a", "b_c"}) {
		t.Errorf("TemplateLabels = %v", got)
	}
}
