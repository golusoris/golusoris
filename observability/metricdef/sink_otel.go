// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package metricdef

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func otelAttributes(labels, values []string) attribute.Set {
	attrs := make([]attribute.KeyValue, 0, len(labels))
	for i, label := range labels {
		attrs = append(attrs, attribute.String(label, values[i]))
	}
	return attribute.NewSet(attrs...)
}

type otelCounter struct {
	instrument metric.Float64Counter
	labels     []string
}

func (s otelCounter) add(ctx context.Context, v float64, values []string) error {
	s.instrument.Add(ctx, v, metric.WithAttributeSet(otelAttributes(s.labels, values)))
	return nil
}

type otelHistogram struct {
	instrument metric.Float64Histogram
	labels     []string
}

func (s otelHistogram) observe(ctx context.Context, v float64, values []string) error {
	s.instrument.Record(ctx, v, metric.WithAttributeSet(otelAttributes(s.labels, values)))
	return nil
}

type otelGaugeSeries struct {
	attrs attribute.Set
	value float64
}

// otelGauge keeps the current value per attribute set and reports it from an
// observable callback, so Add keeps its meaning and the SDK supplies the context.
type otelGauge struct {
	mu     sync.Mutex
	labels []string
	series map[attribute.Distinct]*otelGaugeSeries
}

func newOTelGauge(labels []string) *otelGauge {
	return &otelGauge{labels: labels, series: make(map[attribute.Distinct]*otelGaugeSeries)}
}

func (s *otelGauge) entry(values []string) *otelGaugeSeries {
	attrs := otelAttributes(s.labels, values)
	key := attrs.Equivalent()
	e, ok := s.series[key]
	if !ok {
		e = &otelGaugeSeries{attrs: attrs}
		s.series[key] = e
	}
	return e
}

func (s *otelGauge) set(v float64, values []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entry(values).value = v
	return nil
}

func (s *otelGauge) add(v float64, values []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entry(values).value += v
	return nil
}

func (s *otelGauge) observe(_ context.Context, o metric.Float64Observer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.series {
		o.Observe(e.value, metric.WithAttributeSet(e.attrs))
	}
	return nil
}
