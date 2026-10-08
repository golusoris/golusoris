// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package rules

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/model"
)

// SLOLabel carries the SLO name on every recorded SLI series.
const SLOLabel = "slo"

// SLI yields the error ratio (bad events / all events) over a rate window,
// e.g. "5m", as PromQL.
type SLI interface {
	ErrorRatio(window string) string
}

// EventsSLI is a request-based SLI over two counter selectors, e.g.
// Errors `http_server_request_duration_seconds_count{http_response_status_code=~"5.."}`
// and Total `http_server_request_duration_seconds_count`.
type EventsSLI struct {
	Errors string
	Total  string
}

// ErrorRatio implements [SLI].
func (s EventsSLI) ErrorRatio(window string) string {
	return "sum(rate(" + s.Errors + "[" + window + "])) / sum(rate(" + s.Total + "[" + window + "]))"
}

// LatencySLI counts requests slower than Threshold as bad, from a histogram.
// Threshold must equal one of the histogram's bucket bounds.
type LatencySLI struct {
	// Histogram is the base metric name, e.g. "http_server_request_duration_seconds".
	Histogram string
	// Matchers is an optional matcher list without braces, e.g. `http_route!=""`.
	Matchers  string
	Threshold float64
}

// ErrorRatio implements [SLI]: 1 - fast/all.
func (s LatencySLI) ErrorRatio(window string) string {
	fast := s.selector("_bucket", leMatcher(s.Threshold))
	all := s.selector("_count", "")
	return "1 - (sum(rate(" + fast + "[" + window + "])) / sum(rate(" + all + "[" + window + "])))"
}

func (s LatencySLI) selector(suffix, extra string) string {
	parts := make([]string, 0, 2)
	if s.Matchers != "" {
		parts = append(parts, s.Matchers)
	}
	if extra != "" {
		parts = append(parts, extra)
	}
	if len(parts) == 0 {
		return s.Histogram + suffix
	}
	return s.Histogram + suffix + "{" + strings.Join(parts, ", ") + "}"
}

// leMatcher matches a bucket bound in both exposition spellings: classic text
// writes "1", OpenMetrics and Prometheus 3 normalisation write "1.0".
func leMatcher(threshold float64) string {
	v := strconv.FormatFloat(threshold, 'f', -1, 64)
	if threshold != math.Trunc(threshold) {
		return `le="` + v + `"`
	}
	return `le=~"` + v + `|` + v + `\\.0"`
}

// burnWindow is one multi-window, multi-burn-rate condition: the long and
// short windows must both burn faster than Factor times the error budget.
type burnWindow struct {
	long, short time.Duration
	factor      float64
}

// Windows and factors from the Google SRE workbook, "Alerting on SLOs"
// (multiwindow, multi-burn-rate alerts): spending 2% of a 30-day budget in
// 1h or 5% in 6h pages; 10% in 1d or 3d opens a ticket.
var (
	pageWindows   = []burnWindow{{time.Hour, 5 * time.Minute, 14.4}, {6 * time.Hour, 30 * time.Minute, 6}}
	ticketWindows = []burnWindow{{24 * time.Hour, 2 * time.Hour, 3}, {72 * time.Hour, 6 * time.Hour, 1}}
)

var sloNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// SLO generates recording rules for the error ratio over every alert window
// and two burn-rate alerts: <AlertName>ErrorBudgetBurnFast (critical, pages)
// and <AlertName>ErrorBudgetBurnSlow (warning, ticket).
type SLO struct {
	// Name is the slo label value, e.g. "api_availability".
	Name string
	// AlertName prefixes the alert names, e.g. "APIAvailability".
	AlertName string
	// Objective is the target good ratio, 0 < Objective < 1 (0.999).
	Objective  float64
	SLI        SLI
	RunbookURL string
	// Labels are added to both alerts (team, service).
	Labels map[string]string
}

func (s SLO) validate() error {
	var errs []error
	if !sloNameRE.MatchString(s.Name) {
		errs = append(errs, fmt.Errorf("slo name %q must match %s", s.Name, sloNameRE))
	}
	if !alertNameRE.MatchString(s.AlertName) {
		errs = append(errs, fmt.Errorf("alert name prefix %q must match %s", s.AlertName, alertNameRE))
	}
	if !(s.Objective > 0 && s.Objective < 1) {
		errs = append(errs, fmt.Errorf("objective %v outside (0, 1)", s.Objective))
	}
	if s.SLI == nil {
		errs = append(errs, errors.New("SLI is required"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("rules: slo %s: %w", s.Name, err)
	}
	return nil
}

// RecordName is the recording-rule name of the error ratio over window.
func RecordName(window time.Duration) string {
	return "slo:sli_error:ratio_rate" + model.Duration(window).String()
}

// Group returns the SLO's recording rules and burn-rate alerts as one group
// named "slo-<Name>".
func (s SLO) Group() (Group, error) {
	if err := s.validate(); err != nil {
		return Group{}, err
	}
	g := Group{Name: "slo-" + s.Name}
	for _, w := range sloWindows() {
		g.Records = append(g.Records, Record{
			Name:   RecordName(w),
			Expr:   s.SLI.ErrorRatio(model.Duration(w).String()),
			Labels: map[string]string{SLOLabel: s.Name},
		})
	}
	g.Alerts = []Alert{
		s.burnAlert("ErrorBudgetBurnFast", SeverityCritical, pageWindows, "is burning its error budget fast"),
		s.burnAlert("ErrorBudgetBurnSlow", SeverityWarning, ticketWindows, "is burning its error budget"),
	}
	if err := g.Validate(); err != nil {
		return Group{}, err
	}
	return g, nil
}

// sloWindows lists every distinct window the alerts read, short to long.
func sloWindows() []time.Duration {
	return []time.Duration{5 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 6 * time.Hour, 24 * time.Hour, 72 * time.Hour}
}

func (s SLO) burnAlert(suffix string, sev Severity, windows []burnWindow, verb string) Alert {
	budget := formatRatio(1 - s.Objective)
	conds := make([]string, 0, len(windows))
	for _, w := range windows {
		threshold := "(" + strconv.FormatFloat(w.factor, 'f', -1, 64) + " * " + budget + ")"
		conds = append(conds, "("+s.ratio(w.long)+" > "+threshold+" and "+s.ratio(w.short)+" > "+threshold+")")
	}
	labels := make(map[string]string, len(s.Labels)+1)
	maps.Copy(labels, s.Labels)
	labels[SLOLabel] = s.Name
	return Alert{
		Name:        s.AlertName + suffix,
		Expr:        strings.Join(conds, " or "),
		Severity:    sev,
		Summary:     "SLO " + s.Name + " " + verb,
		Description: "Error ratio is above the burn-rate threshold of objective " + formatRatio(s.Objective) + " in both a long and a short window.",
		RunbookURL:  s.RunbookURL,
		Labels:      labels,
	}
}

func (s SLO) ratio(window time.Duration) string {
	return RecordName(window) + `{` + SLOLabel + `="` + s.Name + `"}`
}
