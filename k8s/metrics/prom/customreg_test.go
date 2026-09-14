// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package prom_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/k8s/metrics/prom"
	"github.com/golusoris/golusoris/observability/statuspage"
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
	if err := prom.MountFor(mux, reg, nil); err != nil {
		t.Fatalf("MountFor: %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestMountForRegistrationConflict is the negative path of MountFor: a
// registry that already holds a *different* descriptor under the same
// fully-qualified name (here: a different help string) is a real
// registration failure, not an "already registered" duplicate, so MountFor
// must return it and must not mount /metrics.
func TestMountForRegistrationConflict(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: "app_check_status", Help: "conflicting help"},
		[]string{"name"},
	))
	mux := http.NewServeMux()

	err := prom.MountFor(mux, reg, statuspage.NewRegistry(clock.NewFake()))
	if err == nil {
		t.Fatal("MountFor: want registration-conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "prom: register check gauge") {
		t.Errorf("error = %q, want prom: register check gauge prefix", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("/metrics mounted despite registration failure: status = %d, want 404", rec.Code)
	}
}
