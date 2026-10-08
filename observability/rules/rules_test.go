// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package rules_test

import (
	"strings"
	"testing"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/golusoris/golusoris/observability/rules"
)

const runbook = "https://runbooks.example.org/node-down"

func nodeDown() rules.Alert {
	return rules.Alert{
		Name:        "NodeDown",
		Expr:        `up{job="node"} == 0`,
		For:         5 * time.Minute,
		Severity:    rules.SeverityCritical,
		Summary:     "Node {{ $labels.instance }} is down",
		Description: "No scrape for 5 minutes.",
		RunbookURL:  runbook,
		Labels:      map[string]string{"team": "platform"},
		Annotations: map[string]string{"dashboard": "https://grafana.example.org/d/nodes"},
	}
}

func jobsGroup() rules.Group {
	return rules.Group{
		Name:     "jobs",
		Interval: 30 * time.Second,
		Records:  []rules.Record{{Name: "job:app_jobs:rate5m", Expr: `sum by (job) (rate(app_jobs_total[5m]))`}},
		Alerts:   []rules.Alert{nodeDown()},
	}
}

func TestRenderPrometheusRule(t *testing.T) {
	t.Parallel()
	raw, err := rules.RenderPrometheusRule(metav1.ObjectMeta{Name: "app", Namespace: "monitoring", Labels: map[string]string{"release": "kps"}}, jobsGroup())
	if err != nil {
		t.Fatalf("RenderPrometheusRule: %v", err)
	}
	var pr monitoringv1.PrometheusRule
	if err := yaml.UnmarshalStrict(raw, &pr); err != nil {
		t.Fatalf("round trip: %v\n%s", err, raw)
	}
	if pr.APIVersion != "monitoring.coreos.com/v1" || pr.Kind != "PrometheusRule" || pr.Name != "app" {
		t.Fatalf("type/meta = %s %s %s", pr.APIVersion, pr.Kind, pr.Name)
	}
	if strings.Contains(string(raw), "creationTimestamp") || strings.Contains(string(raw), "status") {
		t.Errorf("server-side fields rendered:\n%s", raw)
	}
	g := pr.Spec.Groups[0]
	if *g.Interval != "30s" || len(g.Rules) != 2 || g.Rules[0].Record == "" {
		t.Fatalf("group = %+v (records must precede alerts)", g)
	}
	alert := g.Rules[1]
	if *alert.For != "5m" || alert.Labels["severity"] != "critical" || alert.Labels["team"] != "platform" {
		t.Errorf("alert labels/for = %v %v", alert.Labels, *alert.For)
	}
	for k, want := range map[string]string{"runbook_url": runbook, "summary": "Node {{ $labels.instance }} is down", "description": "No scrape for 5 minutes.", "dashboard": "https://grafana.example.org/d/nodes"} {
		if alert.Annotations[k] != want {
			t.Errorf("annotation %s = %q, want %q", k, alert.Annotations[k], want)
		}
	}
}

func TestRenderRuleFileIsPlainPrometheusFormat(t *testing.T) {
	t.Parallel()
	a := nodeDown()
	a.For, a.Description = 0, ""
	raw, err := rules.RenderRuleFile(rules.Group{Name: "plain", Alerts: []rules.Alert{a}})
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "groups:\n") || strings.Contains(text, "apiVersion") {
		t.Fatalf("not a plain rule file:\n%s", text)
	}
	for _, absent := range []string{"for:", "interval:", "description:"} {
		if strings.Contains(text, absent) {
			t.Errorf("zero-valued %s rendered:\n%s", absent, text)
		}
	}
}

func TestAlertValidateRejects(t *testing.T) {
	t.Parallel()
	mutate := func(f func(*rules.Alert)) rules.Alert {
		a := nodeDown()
		f(&a)
		return a
	}
	cases := []struct {
		want  string
		alert rules.Alert
	}{
		{"runbook URL is required", mutate(func(a *rules.Alert) { a.RunbookURL = "" })},
		{"absolute http(s) URL", mutate(func(a *rules.Alert) { a.RunbookURL = "/runbooks/node-down" })},
		{"absolute http(s) URL", mutate(func(a *rules.Alert) { a.RunbookURL = "ftp://x/y" })},
		{"runbook URL:", mutate(func(a *rules.Alert) { a.RunbookURL = "http://[::1" })},
		{"severity", mutate(func(a *rules.Alert) { a.Severity = "page" })},
		{"expr is required", mutate(func(a *rules.Alert) { a.Expr = "" })},
		{"summary is required", mutate(func(a *rules.Alert) { a.Summary = "" })},
		{"alert name", mutate(func(a *rules.Alert) { a.Name = "node down" })},
		{`label "severity" is set by the builder`, mutate(func(a *rules.Alert) { a.Labels = map[string]string{"severity": "x"} })},
		{"invalid label name", mutate(func(a *rules.Alert) { a.Labels = map[string]string{"a-b": "x"} })},
		{`annotation "runbook_url" is set`, mutate(func(a *rules.Alert) { a.Annotations = map[string]string{"runbook_url": "x"} })},
		{"non-negative whole number", mutate(func(a *rules.Alert) { a.For = -time.Second })},
		{"non-negative whole number", mutate(func(a *rules.Alert) { a.For = time.Microsecond })},
	}
	for _, tc := range cases {
		err := tc.alert.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("err = %v, want it to mention %q", err, tc.want)
		}
	}
	if err := nodeDown().Validate(); err != nil {
		t.Errorf("valid alert rejected: %v", err)
	}
}

func TestGroupAndRenderRejects(t *testing.T) {
	t.Parallel()
	meta := metav1.ObjectMeta{Name: "app"}
	cases := map[string]func() error{
		"record without colon": func() error {
			return rules.Record{Name: "app_jobs_rate5m", Expr: "x"}.Validate()
		},
		"record without expr": func() error { return rules.Record{Name: "a:b:c"}.Validate() },
		"record bad label": func() error {
			return rules.Record{Name: "a:b:c", Expr: "x", Labels: map[string]string{"-": ""}}.Validate()
		},
		"empty group":   func() error { return rules.Group{Name: "g"}.Validate() },
		"unnamed group": func() error { return rules.Group{Alerts: []rules.Alert{nodeDown()}}.Validate() },
		"sub-ms interval": func() error {
			g := jobsGroup()
			g.Interval = time.Nanosecond
			return g.Validate()
		},
		"no groups":      func() error { _, err := rules.RenderRuleFile(); return err },
		"duplicate name": func() error { _, err := rules.RenderRuleFile(jobsGroup(), jobsGroup()); return err },
		"no meta name":   func() error { _, err := rules.RenderPrometheusRule(metav1.ObjectMeta{}, jobsGroup()); return err },
		"invalid alert in group": func() error {
			g := jobsGroup()
			g.Alerts[0].RunbookURL = ""
			_, err := rules.RenderPrometheusRule(meta, g)
			return err
		},
		"invalid group in file": func() error { _, err := rules.RenderRuleFile(rules.Group{Name: "g"}); return err },
	}
	for name, run := range cases {
		if run() == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
