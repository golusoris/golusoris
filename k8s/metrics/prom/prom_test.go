package prom_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/k8s/metrics/prom"
	"github.com/golusoris/golusoris/observability/statuspage"
)

func TestHandlerExposesGoMetrics(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	prom.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"go_goroutines", "process_cpu_seconds_total"} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}

func TestMountWiresCheckGauges(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(statuspage.Check{
		Name: "ok-check",
		Fn:   func(context.Context) error { return nil },
	})
	reg.Register(statuspage.Check{
		Name: "down-check",
		Fn:   func(context.Context) error { return errors.New("boom") },
	})
	prom.Mount(r, reg)

	// Run the registry so the OnRun hook fires + gauges populate.
	reg.Run(context.Background())

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rr.Body.String()
	for _, want := range []string{
		`app_check_status{name="ok-check"} 1`,
		`app_check_status{name="down-check"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics missing %q", want)
		}
	}
}
