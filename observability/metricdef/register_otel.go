// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package metricdef

import (
	"errors"
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/otel/metric"

	"github.com/golusoris/golusoris/core/validate"
)

// RegisterOTel validates defs and creates instruments on m with typed handles.
// External defs are refused. Names must round-trip through the default OTel
// Prometheus exporter. A meter cannot roll back instruments on creation errors.
func RegisterOTel(m metric.Meter, defs ...Def) (*Handles, error) {
	if validate.IsNil(m) {
		return nil, errors.New("metricdef: register OTel: nil meter")
	}
	catalog, err := NewCatalog(defs...)
	if err != nil {
		return nil, fmt.Errorf("metricdef: register OTel: %w", err)
	}
	owned := catalog.Defs()
	mappings := make([]otlptranslator.Metric, 0, len(owned))
	for _, d := range owned {
		mapping, mappingErr := otelMapping(d)
		if mappingErr != nil {
			return nil, fmt.Errorf("metricdef: register OTel %s: %w", d.Name, mappingErr)
		}
		mappings = append(mappings, mapping)
	}
	h := &Handles{
		counters:   make(map[string]*Counter, len(owned)),
		gauges:     make(map[string]*Gauge),
		otelGauges: make(map[string]*OTelGauge, len(owned)),
		histograms: make(map[string]*Histogram, len(owned)),
	}
	for i, d := range owned {
		if err = h.addOTel(m, d, mappings[i]); err != nil {
			return nil, fmt.Errorf("metricdef: register OTel %s: %w", d.Name, err)
		}
	}
	return h, nil
}

// RegisterOTel creates instruments for every non-External def of c; see [RegisterOTel].
func (c *Catalog) RegisterOTel(m metric.Meter) (*Handles, error) {
	defs := make([]Def, 0, len(c.names))
	for _, d := range c.Defs() {
		if !d.External {
			defs = append(defs, d)
		}
	}
	return RegisterOTel(m, defs...)
}

func otelMapping(d Def) (otlptranslator.Metric, error) {
	mapping := otlptranslator.Metric{Name: d.Name, Unit: otelUnit(d.Unit)}
	if d.External {
		return mapping, errors.New("external metrics are emitted elsewhere")
	}
	switch d.Kind {
	case KindCounter:
		mapping.Name = strings.TrimSuffix(mapping.Name, "_total")
		mapping.Type = otlptranslator.MetricTypeMonotonicCounter
	case KindGauge:
		mapping.Type = otlptranslator.MetricTypeGauge
	case KindHistogram:
		mapping.Type = otlptranslator.MetricTypeHistogram
	case KindSummary:
		return mapping, errors.New("summaries are emitted elsewhere")
	}
	if d.Unit != "" {
		mapping.Name = strings.TrimSuffix(mapping.Name, "_"+d.Unit)
	}
	namer := otlptranslator.NewMetricNamer("", otlptranslator.UnderscoreEscapingWithSuffixes)
	name, err := namer.Build(mapping)
	if err != nil {
		return mapping, fmt.Errorf("translate name: %w", err)
	}
	if name != d.Name {
		return mapping, fmt.Errorf("unit %q and name cannot round-trip through OTel Prometheus exporter (got %q)", d.Unit, name)
	}
	return mapping, nil
}

func otelUnit(unit string) string {
	switch unit {
	case "seconds":
		return "s"
	case "bytes":
		return "By"
	case "ratio":
		return "1"
	default:
		return unit
	}
}

func (h *Handles) addOTel(m metric.Meter, d Def, mapping otlptranslator.Metric) error {
	guard := newLabelGuard(d)
	switch d.Kind {
	case KindCounter:
		instrument, err := m.Float64Counter(mapping.Name, metric.WithDescription(d.Help), metric.WithUnit(mapping.Unit))
		if err != nil {
			return fmt.Errorf("create counter: %w", err)
		}
		h.counters[d.Name] = &Counter{sink: otelCounter{instrument: instrument, labels: d.Labels}, guard: guard}
	case KindGauge:
		sink := newOTelGauge(d.Labels)
		_, err := m.Float64ObservableGauge(mapping.Name, metric.WithDescription(d.Help), metric.WithUnit(mapping.Unit),
			metric.WithFloat64Callback(sink.observe))
		if err != nil {
			return fmt.Errorf("create gauge: %w", err)
		}
		h.otelGauges[d.Name] = &OTelGauge{sink: sink, guard: guard}
	case KindHistogram:
		buckets := d.Buckets
		if len(buckets) == 0 {
			buckets = prometheus.DefBuckets
		}
		instrument, err := m.Float64Histogram(mapping.Name, metric.WithDescription(d.Help), metric.WithUnit(mapping.Unit),
			metric.WithExplicitBucketBoundaries(buckets...))
		if err != nil {
			return fmt.Errorf("create histogram: %w", err)
		}
		h.histograms[d.Name] = &Histogram{sink: otelHistogram{instrument: instrument, labels: d.Labels}, guard: guard}
	case KindSummary:
		return errors.New("summaries are emitted elsewhere")
	}
	return nil
}

// OTelGauge is a registered OpenTelemetry gauge family.
type OTelGauge struct {
	sink  *otelGauge
	guard *labelGuard
}

// Set sets the series for labelValues to v.
func (g *OTelGauge) Set(v float64, labelValues ...string) error {
	values, err := g.guard.bound(labelValues)
	if err != nil {
		return err
	}
	if err = g.sink.set(v, values); err != nil {
		return fmt.Errorf("metricdef: %s: %w", g.guard.name, err)
	}
	return nil
}

// Add adds v (may be negative) to the series for labelValues.
func (g *OTelGauge) Add(v float64, labelValues ...string) error {
	values, err := g.guard.bound(labelValues)
	if err != nil {
		return err
	}
	if err = g.sink.add(v, values); err != nil {
		return fmt.Errorf("metricdef: %s: %w", g.guard.name, err)
	}
	return nil
}
