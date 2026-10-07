// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package promcheck fails tests when a dashboard panel, template variable,
// annotation or Prometheus rule queries a metric or label that nothing
// emits. It parses every expression with the upstream PromQL parser and
// resolves each selector against a
// [github.com/golusoris/golusoris/observability/metricdef] catalog — written
// by hand, or built from real emission with [CatalogFromGatherer].
//
// Import directly: github.com/golusoris/golusoris/testutil/promcheck (own
// go.mod, so the prometheus/prometheus dependency tree never reaches the
// framework root module).
package promcheck

import (
	"maps"
	"slices"
	"strings"

	"github.com/prometheus/prometheus/promql/parser"

	"github.com/golusoris/golusoris/observability/metricdef"
)

// Expr is one PromQL expression and where it came from.
type Expr struct {
	// Source locates the expression for failure messages.
	Source string
	// Query is the PromQL text; Grafana variables are allowed.
	Query string
	// Record is the series a recording rule defines; other expressions may
	// query it with any labels.
	Record string
	// TemplateLabels are the labels an alert reads as {{ $labels.x }}.
	TemplateLabels []string
	// StaticLabels are labels the rule itself attaches.
	StaticLabels []string
}

// Finding is one unknown metric, unknown label or unparsable expression.
type Finding struct {
	Source  string
	Query   string
	Problem string
}

// String renders the finding for test output.
func (f Finding) String() string {
	return f.Source + ": " + f.Problem + "\n\t" + f.Query
}

// DefaultTargetLabels are attached by every Prometheus scrape.
var DefaultTargetLabels = []string{"job", "instance"}

// defaultSynthetic are series Prometheus itself writes; any labels allowed.
var defaultSynthetic = []string{
	"up", "scrape_duration_seconds", "scrape_samples_scraped",
	"scrape_samples_post_metric_relabeling", "scrape_series_added",
	"ALERTS", "ALERTS_FOR_STATE",
}

type config struct {
	targetLabels map[string]struct{}
	open         map[string]struct{}
}

// Option tunes [Check].
type Option func(*config)

// WithTargetLabels accepts labels that service discovery or relabelling
// attach at scrape time (namespace, pod, service, …) on every metric.
func WithTargetLabels(labels ...string) Option {
	return func(c *config) {
		for _, l := range labels {
			c.targetLabels[l] = struct{}{}
		}
	}
}

// WithOpenSeries accepts series the catalog cannot describe (another
// exporter's metrics) with any labels.
func WithOpenSeries(names ...string) Option {
	return func(c *config) {
		for _, n := range names {
			c.open[n] = struct{}{}
		}
	}
}

func newConfig(opts []Option) config {
	c := config{targetLabels: map[string]struct{}{}, open: map[string]struct{}{}}
	WithTargetLabels(DefaultTargetLabels...)(&c)
	WithOpenSeries(defaultSynthetic...)(&c)
	for _, o := range opts {
		o(&c)
	}
	return c
}

// TB is the subset of testing.TB that [AssertKnownMetrics] reports through.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
}

// AssertKnownMetrics reports every [Finding] of [Check] as a test error.
func AssertKnownMetrics(t TB, exprs []Expr, cat *metricdef.Catalog, opts ...Option) {
	t.Helper()
	for _, f := range Check(exprs, cat, opts...) {
		t.Errorf("promcheck: %s", f)
	}
}

// Check parses every expression and reports selectors naming a metric the
// catalog lacks, label matchers or by() groupings over labels the metric
// does not carry, and {{ $labels.x }} templates no result series carries.
// Recording rules among exprs make their record names known.
func Check(exprs []Expr, cat *metricdef.Catalog, opts ...Option) []Finding {
	c := &checker{cfg: newConfig(opts), cat: cat, parser: parser.NewParser(parser.Options{EnableExperimentalFunctions: true})}
	for _, e := range exprs {
		if e.Record != "" {
			c.cfg.open[e.Record] = struct{}{}
		}
	}
	out := make([]Finding, 0, len(exprs))
	for _, e := range exprs {
		out = append(out, c.check(e)...)
	}
	return out
}

type checker struct {
	cfg    config
	cat    *metricdef.Catalog
	parser parser.Parser
}

// labelSet is a set of label names; open means "any label may appear".
type labelSet struct {
	names map[string]struct{}
	open  bool
}

func newLabelSet() *labelSet { return &labelSet{names: map[string]struct{}{}} }

func (s *labelSet) merge(o *labelSet) {
	s.open = s.open || o.open
	maps.Copy(s.names, o.names)
}

func (s *labelSet) has(name string) bool {
	_, ok := s.names[name]
	return s.open || ok
}

func (s *labelSet) sorted() string {
	return strings.Join(slices.Sorted(maps.Keys(s.names)), ", ")
}
