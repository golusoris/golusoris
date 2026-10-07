// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grafana

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/grafana/grafana-foundation-sdk/go/cog"
	"github.com/grafana/grafana-foundation-sdk/go/common"
	"github.com/grafana/grafana-foundation-sdk/go/dashboard"
	"github.com/grafana/grafana-foundation-sdk/go/prometheus"
	"github.com/grafana/grafana-foundation-sdk/go/stat"
	"github.com/grafana/grafana-foundation-sdk/go/timeseries"

	"github.com/golusoris/golusoris/observability/metricdef"
)

// Threshold is one colour step at or above Value; the green base step
// below the first threshold is implicit.
type Threshold struct {
	Value float64
	Color string
}

// Link is a drill-down link. URL may use Grafana variables such as
// ${__url_time_range} or ${tenant}.
type Link struct {
	Title string
	URL   string
}

// PanelOptions tunes a generated panel. Zero values fall back to the Def.
type PanelOptions struct {
	// Title defaults to Def.Help.
	Title       string
	Description string
	// Unit overrides the unit derived from Def.Unit.
	Unit string
	// Legend overrides the legend format, default "{{label}} …" over Query.By.
	Legend     string
	Thresholds []Threshold
	Links      []Link
}

// Thresholds returns an absolute thresholds config: green base, then steps.
func Thresholds(steps ...Threshold) *dashboard.ThresholdsConfigBuilder {
	all := make([]dashboard.Threshold, 0, len(steps)+1)
	all = append(all, dashboard.Threshold{Color: "green"})
	for _, s := range steps {
		all = append(all, dashboard.Threshold{Value: cog.ToPtr(s.Value), Color: s.Color})
	}
	return dashboard.NewThresholdsConfigBuilder().Mode(dashboard.ThresholdsModeAbsolute).Steps(all)
}

// Links converts drill-down links for panel or dashboard link lists; they
// carry the current time range and variables.
func Links(links ...Link) []cog.Builder[dashboard.DashboardLink] {
	out := make([]cog.Builder[dashboard.DashboardLink], 0, len(links))
	for _, l := range links {
		out = append(out, dashboard.NewDashboardLinkBuilder(l.Title).
			Type(dashboard.DashboardLinkTypeLink).
			Url(l.URL).
			IncludeVars(true).
			KeepTime(true))
	}
	return out
}

// DashboardURL is the relative URL of the dashboard with uid, for [Link].
func DashboardURL(uid string) string {
	return "/d/" + uid
}

func legendFor(o PanelOptions, q Query, suffix string) string {
	if o.Legend != "" {
		return o.Legend
	}
	parts := make([]string, 0, len(q.By)+1)
	for _, l := range q.By {
		parts = append(parts, "{{"+l+"}}")
	}
	if suffix != "" {
		parts = append(parts, suffix)
	}
	if len(parts) == 0 {
		return q.Def.Name
	}
	return strings.Join(parts, " ")
}

func target(expr, legend string) *prometheus.DataqueryBuilder {
	return prometheus.NewDataqueryBuilder().
		Datasource(PrometheusDatasource()).
		Expr(expr).
		LegendFormat(legend).
		Range()
}

func newTimeseries(def metricdef.Def, o PanelOptions, unit string) *timeseries.PanelBuilder {
	title := o.Title
	if title == "" {
		title = def.Help
	}
	if o.Unit != "" {
		unit = o.Unit
	}
	// SDK defaults hide the legend and leave the tooltip mode empty.
	p := timeseries.NewPanelBuilder().
		Title(title).
		Description(o.Description).
		Datasource(PrometheusDatasource()).
		Unit(unit).
		Legend(common.NewVizLegendOptionsBuilder().
			ShowLegend(true).
			DisplayMode(common.LegendDisplayModeList).
			Placement(common.LegendPlacementBottom)).
		Tooltip(common.NewVizTooltipOptionsBuilder().
			Mode(common.TooltipDisplayModeMulti).
			Sort(common.SortOrderDescending))
	if len(o.Thresholds) > 0 {
		p = p.Thresholds(Thresholds(o.Thresholds...))
	}
	if len(o.Links) > 0 {
		p = p.Links(Links(o.Links...))
	}
	return p
}

// RatePanel plots [RateExpr] (counter rate or histogram observation rate).
func RatePanel(q Query, o PanelOptions) (*timeseries.PanelBuilder, error) {
	expr, err := RateExpr(q)
	if err != nil {
		return nil, err
	}
	unit := RateUnit(q.Def.Unit)
	if q.Def.Kind == metricdef.KindHistogram {
		unit = "ops"
	}
	return newTimeseries(q.Def, o, unit).WithTarget(target(expr, legendFor(o, q, ""))), nil
}

// QuantilePanel plots one [QuantileExpr] series per phi with exemplars on,
// so latency points open their traces.
func QuantilePanel(q Query, o PanelOptions, phis ...float64) (*timeseries.PanelBuilder, error) {
	if len(phis) == 0 {
		return nil, fmt.Errorf("grafana: %s: quantile panel needs at least one quantile", q.Def.Name)
	}
	p := newTimeseries(q.Def, o, ValueUnit(q.Def.Unit))
	for _, phi := range phis {
		expr, err := QuantileExpr(q, phi)
		if err != nil {
			return nil, err
		}
		label := "p" + strconv.FormatFloat(phi*100, 'f', -1, 64)
		p = p.WithTarget(target(expr, legendFor(o, q, label)).Exemplar(true))
	}
	return p, nil
}

// ValuePanel plots a gauge over time ([ValueExpr]).
func ValuePanel(q Query, o PanelOptions) (*timeseries.PanelBuilder, error) {
	expr, err := ValueExpr(q)
	if err != nil {
		return nil, err
	}
	return newTimeseries(q.Def, o, ValueUnit(q.Def.Unit)).WithTarget(target(expr, legendFor(o, q, ""))), nil
}

// RatioPanel plots [RatioExpr] as a 0–1 ratio (error rate, hit ratio).
func RatioPanel(num, den Query, o PanelOptions) (*timeseries.PanelBuilder, error) {
	expr, err := RatioExpr(num, den)
	if err != nil {
		return nil, err
	}
	return newTimeseries(num.Def, o, "percentunit").Min(0).WithTarget(target(expr, legendFor(o, num, ""))), nil
}

// StatPanel shows one number: a gauge's current value or a counter's
// increase over the dashboard range. Thresholds colour the background.
func StatPanel(q Query, o PanelOptions) (*stat.PanelBuilder, error) {
	expr, unit, err := statExpr(q)
	if err != nil {
		return nil, err
	}
	title := o.Title
	if title == "" {
		title = q.Def.Help
	}
	if o.Unit != "" {
		unit = o.Unit
	}
	p := stat.NewPanelBuilder().
		Title(title).
		Description(o.Description).
		Datasource(PrometheusDatasource()).
		Unit(unit).
		GraphMode(common.BigValueGraphModeArea).
		WithTarget(target(expr, legendFor(o, q, "")))
	if len(o.Thresholds) > 0 {
		p = p.Thresholds(Thresholds(o.Thresholds...)).ColorMode(common.BigValueColorModeBackground)
	}
	if len(o.Links) > 0 {
		p = p.Links(Links(o.Links...))
	}
	return p, nil
}

func statExpr(q Query) (string, string, error) {
	if q.Def.Kind == metricdef.KindCounter {
		expr, err := IncreaseExpr(q)
		return expr, "short", err
	}
	expr, err := ValueExpr(q)
	return expr, ValueUnit(q.Def.Unit), err
}

// LabelVariable returns a multi-select variable listing the values of label
// on def's series (label_values), with "All" matching every value.
func LabelVariable(def metricdef.Def, label string) (*dashboard.QueryVariableBuilder, error) {
	if !def.HasLabel(label) {
		return nil, fmt.Errorf("grafana: %s has no label %q", def.Name, label)
	}
	query := "label_values(" + def.Series()[0] + ", " + label + ")"
	return dashboard.NewQueryVariableBuilder(label).
		Label(label).
		Datasource(PrometheusDatasource()).
		Query(dashboard.StringOrMap{String: cog.ToPtr(query)}).
		Definition(query).
		Refresh(dashboard.VariableRefreshOnTimeRangeChanged).
		Sort(dashboard.VariableSortAlphabeticalAsc).
		Multi(true).
		IncludeAll(true).
		AllValue(".*"), nil
}

// Annotation returns a Prometheus annotation (deploys, model changes) that
// marks every time expr returns a sample. promcheck verifies expr.
func Annotation(name, expr, color string) *dashboard.AnnotationQueryBuilder {
	return dashboard.NewAnnotationQueryBuilder().
		Name(name).
		Datasource(PrometheusDatasource()).
		Expr(expr).
		IconColor(color).
		Enable(true).
		TitleFormat(name)
}
