// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package rules_test

import (
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/observability/rules"
)

var availability = rules.EventsSLI{
	Errors: `http_server_request_duration_seconds_count{http_response_status_code=~"5.."}`,
	Total:  `http_server_request_duration_seconds_count`,
}

func TestSLIErrorRatios(t *testing.T) {
	t.Parallel()
	cases := map[string]rules.SLI{
		`sum(rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[5m])) / sum(rate(http_server_request_duration_seconds_count[5m]))`: availability,
		`1 - (sum(rate(http_server_request_duration_seconds_bucket{http_route!="", le="0.5"}[5m])) / sum(rate(http_server_request_duration_seconds_count{http_route!=""}[5m])))`: rules.LatencySLI{
			Histogram: "http_server_request_duration_seconds", Matchers: `http_route!=""`, Threshold: 0.5,
		},
		// Integer bounds match both "1" (classic text) and "1.0" (OpenMetrics).
		`1 - (sum(rate(job_seconds_bucket{le=~"1|1\\.0"}[5m])) / sum(rate(job_seconds_count[5m])))`: rules.LatencySLI{Histogram: "job_seconds", Threshold: 1},
	}
	for want, sli := range cases {
		if got := sli.ErrorRatio("5m"); got != want {
			t.Errorf("got  %s\nwant %s", got, want)
		}
	}
}

func TestSLOGroup(t *testing.T) {
	t.Parallel()
	g, err := rules.SLO{
		Name: "api_availability", AlertName: "APIAvailability", Objective: 0.999,
		SLI: availability, RunbookURL: runbook, Labels: map[string]string{"team": "api"},
	}.Group()
	if err != nil {
		t.Fatalf("Group: %v", err)
	}
	if g.Name != "slo-api_availability" || len(g.Records) != 7 || len(g.Alerts) != 2 {
		t.Fatalf("group = %s, %d records, %d alerts", g.Name, len(g.Records), len(g.Alerts))
	}
	wantRecords := []string{"5m", "30m", "1h", "2h", "6h", "1d", "3d"}
	for i, w := range wantRecords {
		if r := g.Records[i]; r.Name != "slo:sli_error:ratio_rate"+w || r.Labels["slo"] != "api_availability" || !strings.Contains(r.Expr, "["+w+"]") {
			t.Errorf("record %d = %+v", i, r)
		}
	}
	fast := g.Alerts[0]
	wantFast := `(slo:sli_error:ratio_rate1h{slo="api_availability"} > (14.4 * 0.001) and slo:sli_error:ratio_rate5m{slo="api_availability"} > (14.4 * 0.001))` +
		` or (slo:sli_error:ratio_rate6h{slo="api_availability"} > (6 * 0.001) and slo:sli_error:ratio_rate30m{slo="api_availability"} > (6 * 0.001))`
	if fast.Name != "APIAvailabilityErrorBudgetBurnFast" || fast.Severity != rules.SeverityCritical || fast.Expr != wantFast {
		t.Errorf("fast alert = %s %s\n%s", fast.Name, fast.Severity, fast.Expr)
	}
	slow := g.Alerts[1]
	if slow.Severity != rules.SeverityWarning || !strings.Contains(slow.Expr, "ratio_rate3d") || !strings.Contains(slow.Expr, "(3 * 0.001)") {
		t.Errorf("slow alert = %+v", slow)
	}
	if fast.Labels["slo"] != "api_availability" || fast.Labels["team"] != "api" || fast.RunbookURL != runbook {
		t.Errorf("alert labels/runbook = %v %s", fast.Labels, fast.RunbookURL)
	}
	if _, err := rules.RenderRuleFile(g); err != nil {
		t.Errorf("SLO group does not render: %v", err)
	}
}

func TestSLOGroupRejects(t *testing.T) {
	t.Parallel()
	base := rules.SLO{Name: "a", AlertName: "A", Objective: 0.99, SLI: availability, RunbookURL: runbook}
	cases := map[string]func(*rules.SLO){
		"objective 1":     func(s *rules.SLO) { s.Objective = 1 },
		"objective 0":     func(s *rules.SLO) { s.Objective = 0 },
		"missing SLI":     func(s *rules.SLO) { s.SLI = nil },
		"bad name":        func(s *rules.SLO) { s.Name = "API availability" },
		"missing runbook": func(s *rules.SLO) { s.RunbookURL = "" },
		"bad alert name":  func(s *rules.SLO) { s.AlertName = "" },
	}
	for name, mut := range cases {
		s := base
		mut(&s)
		if _, err := s.Group(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if got := rules.RecordName(90 * time.Minute); got != "slo:sli_error:ratio_rate1h30m" {
		t.Errorf("RecordName(90m) = %s", got)
	}
}
