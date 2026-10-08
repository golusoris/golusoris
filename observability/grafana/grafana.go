// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package grafana builds Grafana dashboards in Go from
// [github.com/golusoris/golusoris/observability/metricdef] definitions, on
// the Grafana Foundation SDK. Panels derive their PromQL, units and
// variables from the same Def the service registers, so a dashboard cannot
// drift from what is emitted; testutil/promcheck verifies the rendered JSON.
//
// Stateless: no fx module. Generators call the helpers, chain any further
// Foundation SDK builder methods, then [Render] the dashboard to JSON for
// provisioning (Grafana sidecar ConfigMaps, file provisioning).
package grafana

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/grafana/grafana-foundation-sdk/go/cog"
	"github.com/grafana/grafana-foundation-sdk/go/common"
	"github.com/grafana/grafana-foundation-sdk/go/dashboard"
)

// DatasourceVar is the dashboard variable every helper's datasource refers to.
const DatasourceVar = "datasource"

// PrometheusDatasource references the Prometheus datasource chosen in the
// ${datasource} variable.
func PrometheusDatasource() common.DataSourceRef {
	// Locals, not new(expr): Semgrep's Go parser rejects new with a value (semgrep/semgrep#11972).
	kind, uid := "prometheus", "${"+DatasourceVar+"}"
	return common.DataSourceRef{Type: &kind, Uid: &uid}
}

// NewDashboard returns a dashboard builder with the ${datasource} variable,
// shared crosshair, 30s refresh and a 6h window. uid must be stable: Grafana
// links and provisioning key on it.
//
// It emits the classic (v1) JSON model on purpose: classic file provisioning
// (Grafana sidecar ConfigMaps) and dashboard-linter read v1; the SDK's
// dashboardv2 targets the new resource API.
//
//nolint:staticcheck // v1 model is what classic provisioning loads.
func NewDashboard(uid, title string, tags ...string) *dashboard.DashboardBuilder {
	return dashboard.NewDashboardBuilder(title).
		Uid(uid).
		Tags(tags).
		Refresh("30s").
		Time("now-6h", "now").
		Timezone(common.TimeZoneBrowser).
		Tooltip(dashboard.DashboardCursorSyncCrosshair).
		WithVariable(dashboard.NewDatasourceVariableBuilder(DatasourceVar).
			Label("Data source").
			Type("prometheus"))
}

// Render builds b and returns indented dashboard JSON ending in a newline.
//
//nolint:staticcheck // v1 model is what classic provisioning loads.
func Render(b cog.Builder[dashboard.Dashboard]) ([]byte, error) {
	if b == nil {
		return nil, errors.New("grafana: render: nil builder")
	}
	d, err := b.Build()
	if err != nil {
		return nil, fmt.Errorf("grafana: render: %w", err)
	}
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("grafana: render: %w", err)
	}
	return append(out, '\n'), nil
}

// unitByMetric maps metricdef base units to Grafana value units.
var unitByMetric = map[string]string{
	"seconds": "s",
	"bytes":   "bytes",
	"ratio":   "percentunit",
	"celsius": "celsius",
	"volts":   "volt",
	"joules":  "joule",
	"watts":   "watt",
	"hertz":   "hertz",
}

// rateUnitByMetric maps base units to per-second Grafana units.
var rateUnitByMetric = map[string]string{
	"bytes": "Bps",
}

// ValueUnit returns the Grafana unit for a value in metricdef unit u
// ("short" when unknown or unitless).
func ValueUnit(u string) string {
	if v, ok := unitByMetric[u]; ok {
		return v
	}
	return "short"
}

// RateUnit returns the Grafana unit for a per-second rate of unit u
// ("ops" when no dedicated rate unit exists).
func RateUnit(u string) string {
	if v, ok := rateUnitByMetric[u]; ok {
		return v
	}
	return "ops"
}
