// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package promcheck_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/httpx/middleware"
	"github.com/golusoris/golusoris/httpx/router"
	"github.com/golusoris/golusoris/k8s/metrics/prom"
	"github.com/golusoris/golusoris/observability/metricdef"
	"github.com/golusoris/golusoris/observability/statuspage"
	"github.com/golusoris/golusoris/otel"
	"github.com/golusoris/golusoris/testutil/promcheck"
)

// deployDir holds the shipped dashboard and rules.
var deployDir = filepath.Join("..", "..", "deploy", "observability")

// kubeTargetLabels are attached by kube-prometheus ServiceMonitor relabelling;
// the shipped dashboard filters on namespace and pod.
var kubeTargetLabels = []string{"namespace", "pod", "service", "container", "endpoint"}

// emittedCatalog runs what a golusoris app exposes — Go and process
// collectors, otelhttp server metrics of a chi router through the OTel
// Prometheus reader, and the check-status gauges — and describes the
// resulting families.
func emittedCatalog(t *testing.T) *metricdef.Catalog {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	providers, err := otel.NewWithRegisterer(t.Context(), otel.Options{
		Enabled: true,
		Service: otel.ServiceOptions{Name: "promcheck"},
		Export:  otel.ExportOptions{Prometheus: true},
	}, reg)
	if err != nil {
		t.Fatalf("otel: %v", err)
	}
	t.Cleanup(func() {
		if shutdownErr := providers.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("otel shutdown: %v", shutdownErr)
		}
	})
	// The canonical stack: OTel installed with Use on the chi router.
	mux := router.New()
	mux.Use(middleware.OTel("app", nil))
	mux.Get("/ok", func(http.ResponseWriter, *http.Request) {})
	mux.Get("/fail", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	for _, path := range []string{"/ok", "/fail"} {
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	}

	checks := statuspage.NewRegistry(clock.NewFake())
	checks.Register(statuspage.Check{Name: "db", Fn: func(context.Context) error { return errors.New("down") }})
	if mountErr := prom.MountFor(http.NewServeMux(), reg, checks); mountErr != nil {
		t.Fatalf("mount: %v", mountErr)
	}
	checks.Run(t.Context())

	cat, err := promcheck.CatalogFromGatherer(reg)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return cat
}

func readExprs(t *testing.T, name string, extract func([]byte, string) ([]promcheck.Expr, error)) []promcheck.Expr {
	t.Helper()
	raw, err := os.ReadFile(name) //nolint:gosec // test reads fixed repository paths.
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	out, err := extract(raw, filepath.Base(name))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("%s yielded no expressions", name)
	}
	return out
}

// TestShippedObservabilityQueriesMatchEmission is the dashboard-vs-emitted
// gate for deploy/observability: every panel, variable and rule must query
// series a golusoris app really exposes.
func TestShippedObservabilityQueriesMatchEmission(t *testing.T) { //nolint:paralleltest // Installs global OTel providers.
	cat := emittedCatalog(t)
	all := readExprs(t, filepath.Join(deployDir, "prometheus-rules.yaml"), promcheck.RuleExprs)
	all = append(all, readExprs(t, filepath.Join(deployDir, "grafana-dashboard-http.json"), promcheck.DashboardExprs)...)
	promcheck.AssertKnownMetrics(t, all, cat, promcheck.WithTargetLabels(kubeTargetLabels...))
}

// TestShippedRuleDefectIsCaught replays the rules file as shipped at 8a0dd70:
// the gate must reject its `status` filter and its $labels.check template.
func TestShippedRuleDefectIsCaught(t *testing.T) { //nolint:paralleltest // Installs global OTel providers.
	cat := emittedCatalog(t)
	planted := readExprs(t, filepath.Join("testdata", "prometheus-rules-shipped-8a0dd70.yaml"), promcheck.RuleExprs)
	findings := promcheck.Check(planted, cat, promcheck.WithTargetLabels(kubeTargetLabels...))
	var text strings.Builder
	for _, f := range findings {
		text.WriteString(f.String() + "\n")
	}
	for _, want := range []string{`label "status" is not on http_server_request_duration_seconds_count`, "template reads $labels.check"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("planted defect %q not reported; findings:\n%s", want, text.String())
		}
	}
	if len(findings) != 3 { // status matcher + check in summary and description
		t.Errorf("got %d findings, want 3:\n%s", len(findings), text.String())
	}
}
