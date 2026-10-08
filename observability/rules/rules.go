// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package rules builds Prometheus alerting and recording rules — as a
// prometheus-operator PrometheusRule (monitoring.coreos.com/v1) or a plain
// rule file for `promtool test rules` — with a runbook URL required on every
// alert, plus a multi-window, multi-burn-rate SLO helper ([SLO]).
//
// Stateless: no fx module. Generators build [Group] values and render them;
// testutil/promcheck verifies every expression against the metric catalog.
package rules

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/common/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"
)

// Severity is the alert's severity label.
type Severity string

// Severity values.
const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// Label and annotation keys the builders own.
const (
	SeverityLabel        = "severity"
	SummaryAnnotation    = "summary"
	DescriptionAnno      = "description"
	RunbookURLAnnotation = "runbook_url"
)

var (
	alertNameRE  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	recordNameRE = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelNameRE  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// Alert is one alerting rule.
type Alert struct {
	// Name is the alertname, e.g. "NodeDown".
	Name string
	// Expr is the PromQL condition.
	Expr string
	// For is how long Expr must hold before firing; 0 fires at once.
	For      time.Duration
	Severity Severity
	// Summary is the one-line page text; it may use {{ $labels.x }}.
	Summary     string
	Description string
	// RunbookURL is required: an absolute http(s) link to the response page.
	RunbookURL  string
	Labels      map[string]string
	Annotations map[string]string
}

// Validate reports every problem with a.
func (a Alert) Validate() error {
	var errs []error
	if !alertNameRE.MatchString(a.Name) {
		errs = append(errs, fmt.Errorf("alert name %q must match %s", a.Name, alertNameRE))
	}
	if a.Expr == "" {
		errs = append(errs, errors.New("expr is required"))
	}
	if !slices.Contains([]Severity{SeverityCritical, SeverityWarning, SeverityInfo}, a.Severity) {
		errs = append(errs, fmt.Errorf("severity %q not one of critical, warning, info", a.Severity))
	}
	if a.Summary == "" {
		errs = append(errs, errors.New("summary is required"))
	}
	errs = append(errs, validateRunbook(a.RunbookURL), validateDuration("for", a.For))
	errs = append(errs, validateKeys("label", a.Labels, SeverityLabel, "alertname"))
	errs = append(errs, validateKeys("annotation", a.Annotations, SummaryAnnotation, DescriptionAnno, RunbookURLAnnotation))
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("rules: alert %s: %w", a.Name, err)
	}
	return nil
}

func validateRunbook(raw string) error {
	if raw == "" {
		return errors.New("runbook URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("runbook URL: %w", err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("runbook URL %q must be an absolute http(s) URL", raw)
	}
	return nil
}

// validateDuration accepts whole milliseconds >= 0, the Prometheus grammar.
func validateDuration(field string, d time.Duration) error {
	if d < 0 || d%time.Millisecond != 0 {
		return fmt.Errorf("%s %v must be a non-negative whole number of milliseconds", field, d)
	}
	return nil
}

func validateKeys(kind string, m map[string]string, reserved ...string) error {
	for k := range m {
		if !labelNameRE.MatchString(k) {
			return fmt.Errorf("invalid %s name %q", kind, k)
		}
		if slices.Contains(reserved, k) {
			return fmt.Errorf("%s %q is set by the builder", kind, k)
		}
	}
	return nil
}

// Record is one recording rule.
type Record struct {
	// Name follows level:metric:operations and must contain a colon.
	Name   string
	Expr   string
	Labels map[string]string
}

// Validate reports every problem with r.
func (r Record) Validate() error {
	var errs []error
	if !recordNameRE.MatchString(r.Name) || !strings.Contains(r.Name, ":") {
		errs = append(errs, fmt.Errorf("record name %q must be a metric name of the form level:metric:operations", r.Name))
	}
	if r.Expr == "" {
		errs = append(errs, errors.New("expr is required"))
	}
	errs = append(errs, validateKeys("label", r.Labels))
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("rules: record %s: %w", r.Name, err)
	}
	return nil
}

// Group is one rule group; Records render before Alerts so alerts can use
// series recorded in the same evaluation.
type Group struct {
	Name string
	// Interval overrides the global evaluation interval; 0 keeps it.
	Interval time.Duration
	Records  []Record
	Alerts   []Alert
}

// Validate reports every problem with g and its rules.
func (g Group) Validate() error {
	errs := []error{validateDuration("interval", g.Interval)}
	if g.Name == "" {
		errs = append(errs, errors.New("group name is required"))
	}
	if len(g.Records)+len(g.Alerts) == 0 {
		errs = append(errs, errors.New("group has no rules"))
	}
	for _, r := range g.Records {
		errs = append(errs, r.Validate())
	}
	for _, a := range g.Alerts {
		errs = append(errs, a.Validate())
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("rules: group %q: %w", g.Name, err)
	}
	return nil
}

func promDuration(d time.Duration) *monitoringv1.Duration {
	if d == 0 {
		return nil
	}
	s := monitoringv1.Duration(model.Duration(d).String())
	return &s
}

// RuleGroup converts g to the prometheus-operator type after validating it.
func (g Group) RuleGroup() (monitoringv1.RuleGroup, error) {
	if err := g.Validate(); err != nil {
		return monitoringv1.RuleGroup{}, err
	}
	out := monitoringv1.RuleGroup{Name: g.Name, Interval: promDuration(g.Interval), Rules: make([]monitoringv1.Rule, 0, len(g.Records)+len(g.Alerts))}
	for _, r := range g.Records {
		out.Rules = append(out.Rules, monitoringv1.Rule{Record: r.Name, Expr: intstr.FromString(r.Expr), Labels: r.Labels})
	}
	for _, a := range g.Alerts {
		out.Rules = append(out.Rules, alertRule(a))
	}
	return out, nil
}

func alertRule(a Alert) monitoringv1.Rule {
	labels := make(map[string]string, len(a.Labels)+1)
	maps.Copy(labels, a.Labels)
	labels[SeverityLabel] = string(a.Severity)
	annotations := make(map[string]string, len(a.Annotations)+3)
	maps.Copy(annotations, a.Annotations)
	annotations[SummaryAnnotation] = a.Summary
	annotations[RunbookURLAnnotation] = a.RunbookURL
	if a.Description != "" {
		annotations[DescriptionAnno] = a.Description
	}
	return monitoringv1.Rule{Alert: a.Name, Expr: intstr.FromString(a.Expr), For: promDuration(a.For), Labels: labels, Annotations: annotations}
}

func ruleGroups(groups []Group) ([]monitoringv1.RuleGroup, error) {
	if len(groups) == 0 {
		return nil, errors.New("rules: no groups")
	}
	out := make([]monitoringv1.RuleGroup, 0, len(groups))
	seen := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		if _, dup := seen[g.Name]; dup {
			return nil, fmt.Errorf("rules: duplicate group %q", g.Name)
		}
		seen[g.Name] = struct{}{}
		rg, err := g.RuleGroup()
		if err != nil {
			return nil, err
		}
		out = append(out, rg)
	}
	return out, nil
}

// NewPrometheusRule returns a monitoring.coreos.com/v1 PrometheusRule named
// by meta holding groups.
func NewPrometheusRule(meta metav1.ObjectMeta, groups ...Group) (*monitoringv1.PrometheusRule, error) {
	if meta.Name == "" {
		return nil, errors.New("rules: PrometheusRule needs metadata.name")
	}
	rgs, err := ruleGroups(groups)
	if err != nil {
		return nil, err
	}
	return &monitoringv1.PrometheusRule{
		APIVersion: monitoringv1.SchemeGroupVersion.String(), Kind: monitoringv1.PrometheusRuleKind,
		ObjectMeta: meta,
		Spec:       monitoringv1.PrometheusRuleSpec{Groups: rgs},
	}, nil
}

// RenderPrometheusRule renders [NewPrometheusRule] as YAML.
func RenderPrometheusRule(meta metav1.ObjectMeta, groups ...Group) ([]byte, error) {
	pr, err := NewPrometheusRule(meta, groups...)
	if err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(pr)
	if err != nil {
		return nil, fmt.Errorf("rules: marshal PrometheusRule: %w", err)
	}
	return out, nil
}

// RenderRuleFile renders a plain Prometheus rule file (`groups:`), the
// input `promtool check rules` and `promtool test rules` read.
func RenderRuleFile(groups ...Group) ([]byte, error) {
	rgs, err := ruleGroups(groups)
	if err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(monitoringv1.PrometheusRuleSpec{Groups: rgs})
	if err != nil {
		return nil, fmt.Errorf("rules: marshal rule file: %w", err)
	}
	return out, nil
}

// formatRatio renders a ratio without float noise (1-0.999 -> "0.001").
func formatRatio(v float64) string {
	return fmt.Sprintf("%.12g", math.Round(v*1e12)/1e12)
}
