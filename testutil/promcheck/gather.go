// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package promcheck

import (
	"errors"
	"fmt"
	"slices"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/golusoris/golusoris/observability/metricdef"
)

// kinds maps exposition types to catalog kinds; untyped series read as gauges.
var kinds = map[dto.MetricType]metricdef.Kind{
	dto.MetricType_COUNTER:         metricdef.KindCounter,
	dto.MetricType_GAUGE:           metricdef.KindGauge,
	dto.MetricType_UNTYPED:         metricdef.KindGauge,
	dto.MetricType_HISTOGRAM:       metricdef.KindHistogram,
	dto.MetricType_GAUGE_HISTOGRAM: metricdef.KindHistogram,
	dto.MetricType_SUMMARY:         metricdef.KindSummary,
}

// CatalogFromGatherer gathers g once and describes every family as an
// External def with the label names its samples carry. Exercise the code
// under test first so labelled series exist: a family's labels are only
// known once a sample was recorded.
func CatalogFromGatherer(g prometheus.Gatherer) (*metricdef.Catalog, error) {
	if g == nil {
		return nil, errors.New("promcheck: nil gatherer")
	}
	families, err := g.Gather()
	if err != nil {
		return nil, fmt.Errorf("promcheck: gather: %w", err)
	}
	defs := make([]metricdef.Def, 0, len(families))
	for _, f := range families {
		kind, ok := kinds[f.GetType()]
		if !ok {
			return nil, fmt.Errorf("promcheck: family %s has unsupported type %s", f.GetName(), f.GetType())
		}
		defs = append(defs, metricdef.Def{Name: f.GetName(), Help: f.GetHelp(), Kind: kind, Labels: familyLabels(f), External: true})
	}
	cat, err := metricdef.NewCatalog(defs...)
	if err != nil {
		return nil, fmt.Errorf("promcheck: catalog from gatherer: %w", err)
	}
	return cat, nil
}

// familyLabels returns the sorted label names across f's samples; le and
// quantile come from the kind, not the family.
func familyLabels(f *dto.MetricFamily) []string {
	seen := map[string]struct{}{}
	for _, m := range f.GetMetric() {
		for _, l := range m.GetLabel() {
			seen[l.GetName()] = struct{}{}
		}
	}
	delete(seen, "le")
	delete(seen, "quantile")
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	slices.Sort(out)
	return out
}
