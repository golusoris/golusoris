// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grafana

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/golusoris/golusoris/observability/metricdef"
)

// RateInterval is Grafana's scrape-aware rate window.
const RateInterval = "$__rate_interval"

// Query derives PromQL from one [metricdef.Def]. Every label it names must be
// one of Def.Labels, so a generated panel can only query emitted series.
type Query struct {
	// Def is the metric to query.
	Def metricdef.Def
	// By lists the grouping labels of the outer sum.
	By []string
	// Filters lists labels matched against the same-named dashboard
	// variable: `label=~"$label"`.
	Filters []string
	// Matchers adds fixed regex matchers, label -> RE2 regex.
	Matchers map[string]string
}

func (q Query) validate(kinds ...metricdef.Kind) error {
	if !slices.Contains(kinds, q.Def.Kind) {
		return fmt.Errorf("grafana: %s: kind %s not supported here (want %v)", q.Def.Name, q.Def.Kind, kinds)
	}
	var errs []error
	for _, l := range slices.Concat(q.By, q.Filters) {
		if !q.Def.HasLabel(l) {
			errs = append(errs, fmt.Errorf("grafana: %s has no label %q", q.Def.Name, l))
		}
	}
	for l := range q.Matchers {
		if !q.Def.HasLabel(l) {
			errs = append(errs, fmt.Errorf("grafana: %s has no label %q", q.Def.Name, l))
		}
	}
	return errors.Join(errs...)
}

// selector renders series{filters, matchers} with a deterministic order.
func (q Query) selector(series string) string {
	parts := make([]string, 0, len(q.Filters)+len(q.Matchers))
	for _, l := range q.Filters {
		parts = append(parts, l+`=~"$`+l+`"`)
	}
	keys := make([]string, 0, len(q.Matchers))
	for k := range q.Matchers {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		parts = append(parts, k+"=~"+strconv.Quote(q.Matchers[k]))
	}
	if len(parts) == 0 {
		return series
	}
	return series + "{" + strings.Join(parts, ", ") + "}"
}

func sumBy(by []string, inner string) string {
	if len(by) == 0 {
		return "sum(" + inner + ")"
	}
	return "sum by (" + strings.Join(by, ", ") + ") (" + inner + ")"
}

// RateExpr returns the per-second rate of a counter, or of a histogram's
// observation count: sum by (By) (rate(series{…}[$__rate_interval])).
func RateExpr(q Query) (string, error) {
	if err := q.validate(metricdef.KindCounter, metricdef.KindHistogram); err != nil {
		return "", err
	}
	series := q.Def.Name
	if q.Def.Kind == metricdef.KindHistogram {
		series += "_count"
	}
	return sumBy(q.By, "rate("+q.selector(series)+"["+RateInterval+"])"), nil
}

// IncreaseExpr returns a counter's increase over the dashboard range:
// sum by (By) (increase(series{…}[$__range])).
func IncreaseExpr(q Query) (string, error) {
	if err := q.validate(metricdef.KindCounter); err != nil {
		return "", err
	}
	return sumBy(q.By, "increase("+q.selector(q.Def.Name)+"[$__range])"), nil
}

// QuantileExpr returns histogram_quantile(phi, sum by (le, By) (rate(…_bucket{…}[$__rate_interval]))).
func QuantileExpr(q Query, phi float64) (string, error) {
	if err := q.validate(metricdef.KindHistogram); err != nil {
		return "", err
	}
	if !(phi > 0 && phi < 1) {
		return "", fmt.Errorf("grafana: %s: quantile %v outside (0, 1)", q.Def.Name, phi)
	}
	inner := sumBy(append([]string{"le"}, q.By...), "rate("+q.selector(q.Def.Name+"_bucket")+"["+RateInterval+"])")
	return "histogram_quantile(" + strconv.FormatFloat(phi, 'f', -1, 64) + ", " + inner + ")", nil
}

// ValueExpr returns the current value of a gauge: sum by (By) (series{…}).
func ValueExpr(q Query) (string, error) {
	if err := q.validate(metricdef.KindGauge); err != nil {
		return "", err
	}
	return sumBy(q.By, q.selector(q.Def.Name)), nil
}

// RatioExpr divides two rate expressions, e.g. failed over all requests:
// (RateExpr(num)) / (RateExpr(den)). Both must group by the same labels.
func RatioExpr(num, den Query) (string, error) {
	if !slices.Equal(num.By, den.By) {
		return "", fmt.Errorf("grafana: ratio groups differ: %v vs %v", num.By, den.By)
	}
	n, err := RateExpr(num)
	if err != nil {
		return "", err
	}
	d, err := RateExpr(den)
	if err != nil {
		return "", err
	}
	return "(" + n + ") / (" + d + ")", nil
}
