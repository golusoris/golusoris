// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package metricdef declares Prometheus metrics once — name, help, unit,
// kind, labels, buckets and label-cardinality bounds — so services register
// typed handles from the same [Def] values that dashboard generators
// (observability/grafana), rule builders (observability/rules) and query
// checks (testutil/promcheck) read through [Catalog.Defs].
//
// Stateless: no fx module. Build a [Catalog] at package level, call
// [Catalog.Register] once per process with the app's registerer.
package metricdef

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

// Kind is a metric type.
type Kind int

// Kind values.
const (
	// KindCounter is a monotonically increasing counter; names end in _total.
	KindCounter Kind = iota + 1
	// KindGauge is a value that goes up and down.
	KindGauge
	// KindHistogram is a bucketed distribution exposed as _bucket/_sum/_count.
	KindHistogram
	// KindSummary is a client-side quantile summary (_sum/_count plus
	// quantile series). Only External defs may use it — instrument new code
	// with histograms.
	KindSummary
)

// String returns the Prometheus type name.
func (k Kind) String() string {
	switch k {
	case KindCounter:
		return "counter"
	case KindGauge:
		return "gauge"
	case KindHistogram:
		return "histogram"
	case KindSummary:
		return "summary"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// OtherValue replaces label values that a [LabelLimit] rejects.
const OtherValue = "other"

// LabelLimit bounds the cardinality of one label. Set exactly one field.
type LabelLimit struct {
	// Allow lists the permitted values; any other value becomes [OtherValue].
	Allow []string
	// MaxDistinct admits the first MaxDistinct distinct values per process
	// and folds every later new value into [OtherValue].
	MaxDistinct int
}

// Def declares one metric.
type Def struct {
	// Name is the full metric name, e.g. "vmafx_jobs_total".
	Name string
	// Help is the HELP text.
	Help string
	// Unit is the base unit the name ends with ("seconds", "bytes",
	// "ratio"); empty for unitless metrics.
	Unit string
	// Kind is the metric type.
	Kind Kind
	// Labels are the variable label names, in WithLabelValues order.
	Labels []string
	// Buckets are histogram upper bounds; nil means prometheus.DefBuckets.
	Buckets []float64
	// Limits bounds label cardinality, keyed by label name.
	Limits map[string]LabelLimit
	// External marks a metric another library emits (otelhttp, the Go
	// collector). Generators and promcheck see it; Register refuses it.
	// External defs are checked for syntax only — their names follow the
	// emitter, not this package's conventions — and Help may be empty.
	External bool
}

var (
	metricNameRE   = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	externalNameRE = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelNameRE    = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	unitRE         = regexp.MustCompile(`^[a-z][a-z0-9_]*[a-z0-9]$`)
)

// reservedSuffixes collide with series that histograms, summaries or
// OpenMetrics counters expose.
var reservedSuffixes = []string{"_bucket", "_count", "_sum", "_created"}

// nonBaseUnits violate the Prometheus base-unit naming rule.
var nonBaseUnits = []string{
	"milliseconds", "microseconds", "nanoseconds", "minutes", "hours", "days",
	"kilobytes", "megabytes", "gigabytes", "bits", "percent",
}

// Validate reports every naming, label, bucket and limit violation of d.
// External defs only need a syntactically valid name, kind and labels.
func (d Def) Validate() error {
	errs := []error{d.validateName(), d.validateUnit(), d.validateLabels(), d.validateBuckets(), d.validateLimits()}
	if d.External {
		errs = []error{d.validateExternal(), d.validateLabels(), d.validateLimits()}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("metricdef: %s: %w", d.Name, err)
	}
	return nil
}

func (d Def) validateExternal() error {
	if !externalNameRE.MatchString(d.Name) || strings.HasPrefix(d.Name, "__") {
		return fmt.Errorf("invalid metric name %q", d.Name)
	}
	if d.Kind < KindCounter || d.Kind > KindSummary {
		return fmt.Errorf("unknown kind %d", int(d.Kind))
	}
	return nil
}

func (d Def) validateName() error {
	if !metricNameRE.MatchString(d.Name) || strings.HasPrefix(d.Name, "__") {
		return fmt.Errorf("invalid metric name %q (want [a-zA-Z_][a-zA-Z0-9_]*, no leading __, no colons)", d.Name)
	}
	if d.Help == "" {
		return errors.New("help is required")
	}
	if err := d.validateKindSuffix(); err != nil {
		return err
	}
	base := strings.TrimSuffix(d.Name, "_total")
	for _, suffix := range reservedSuffixes {
		if strings.HasSuffix(base, suffix) {
			return fmt.Errorf("name ends in reserved suffix %s", suffix)
		}
	}
	return nil
}

func (d Def) validateKindSuffix() error {
	counterName := strings.HasSuffix(d.Name, "_total")
	switch d.Kind {
	case KindCounter:
		if !counterName {
			return errors.New("counter names end in _total")
		}
	case KindGauge, KindHistogram:
		if counterName {
			return fmt.Errorf("only counters end in _total, not %s", d.Kind)
		}
	case KindSummary:
		return errors.New("summaries are only supported as External defs; use a histogram")
	default:
		return fmt.Errorf("unknown kind %d", int(d.Kind))
	}
	return nil
}

func (d Def) validateUnit() error {
	if d.Unit == "" {
		return nil
	}
	if !unitRE.MatchString(d.Unit) {
		return fmt.Errorf("invalid unit %q (want lowercase snake case)", d.Unit)
	}
	if slices.Contains(nonBaseUnits, d.Unit) {
		return fmt.Errorf("unit %q is not a base unit (use seconds, bytes or ratio)", d.Unit)
	}
	if !strings.HasSuffix(strings.TrimSuffix(d.Name, "_total"), "_"+d.Unit) {
		return fmt.Errorf("name must end in _%s (before _total for counters)", d.Unit)
	}
	return nil
}

func (d Def) validateLabels() error {
	seen := make(map[string]struct{}, len(d.Labels))
	for _, l := range d.Labels {
		if !labelNameRE.MatchString(l) || strings.HasPrefix(l, "__") {
			return fmt.Errorf("invalid label name %q", l)
		}
		if l == "le" && d.Kind == KindHistogram || l == "quantile" {
			return fmt.Errorf("label %q is reserved", l)
		}
		if _, dup := seen[l]; dup {
			return fmt.Errorf("duplicate label %q", l)
		}
		seen[l] = struct{}{}
	}
	return nil
}

func (d Def) validateBuckets() error {
	if len(d.Buckets) == 0 {
		return nil
	}
	if d.Kind != KindHistogram {
		return errors.New("buckets are only valid on histograms")
	}
	for i, b := range d.Buckets {
		if math.IsNaN(b) || math.IsInf(b, 0) {
			return fmt.Errorf("bucket %d is %v", i, b)
		}
		if i > 0 && b <= d.Buckets[i-1] {
			return fmt.Errorf("buckets must increase strictly (%v after %v)", b, d.Buckets[i-1])
		}
	}
	return nil
}

func (d Def) validateLimits() error {
	for label, limit := range d.Limits {
		if !d.HasLabel(label) {
			return fmt.Errorf("limit on undeclared label %q", label)
		}
		if limit.MaxDistinct < 0 || (len(limit.Allow) == 0) == (limit.MaxDistinct == 0) {
			return fmt.Errorf("limit on %q must set exactly one of Allow or a positive MaxDistinct", label)
		}
	}
	return nil
}

// HasLabel reports whether name is one of d.Labels.
func (d Def) HasLabel(name string) bool {
	return slices.Contains(d.Labels, name)
}

// Series returns the sample names d exposes in the classic text format.
func (d Def) Series() []string {
	switch d.Kind {
	case KindHistogram:
		return []string{d.Name + "_bucket", d.Name + "_count", d.Name + "_sum"}
	case KindSummary:
		return []string{d.Name, d.Name + "_count", d.Name + "_sum"}
	case KindCounter, KindGauge:
	}
	return []string{d.Name}
}

// SeriesLabels returns the labels valid on series (one of [Def.Series]):
// d.Labels, plus "le" on a histogram's _bucket series and "quantile" on a
// summary's quantile series.
func (d Def) SeriesLabels(series string) []string {
	labels := slices.Clone(d.Labels)
	switch {
	case d.Kind == KindHistogram && series == d.Name+"_bucket":
		labels = append(labels, "le")
	case d.Kind == KindSummary && series == d.Name:
		labels = append(labels, "quantile")
	}
	return labels
}

// clone deep-copies the slices and map so callers cannot mutate a catalog.
func (d Def) clone() Def {
	d.Labels = slices.Clone(d.Labels)
	d.Buckets = slices.Clone(d.Buckets)
	if d.Limits != nil {
		limits := make(map[string]LabelLimit, len(d.Limits))
		for k, v := range d.Limits {
			v.Allow = slices.Clone(v.Allow)
			limits[k] = v
		}
		d.Limits = limits
	}
	return d
}
