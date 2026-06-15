package prom_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/golusoris/golusoris/k8s/metrics/prom"
)

func TestHandlerForServesOnlyCustomRegistry(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: "biz_custom_total", Help: "x"})
	reg.MustRegister(c)
	c.Add(3)

	rec := httptest.NewRecorder()
	prom.HandlerFor(reg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "biz_custom_total 3") {
		t.Errorf("custom metric missing from /metrics:\n%s", body)
	}
	if strings.Contains(body, "go_goroutines") {
		t.Error("custom-registry handler leaked default-registry metrics")
	}
}

func TestMountForOnServeMux(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	mux := http.NewServeMux()
	prom.MountFor(mux, reg, nil)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
