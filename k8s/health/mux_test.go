package health_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

func newReg() *statuspage.Registry { return statuspage.NewRegistry(clock.NewFake()) }

func code(t *testing.T, mux *http.ServeMux, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

func TestMountMuxReadyz(t *testing.T) {
	t.Parallel()
	reg := newReg()
	reg.Register(statuspage.Check{Name: "db", Tags: []string{health.TagReadiness}, Fn: func(context.Context) error { return nil }})
	mux := http.NewServeMux()
	health.MountMux(mux, reg)
	if got := code(t, mux, "/readyz"); got != http.StatusOK {
		t.Errorf("/readyz = %d, want 200", got)
	}
}

// TestDegradedReadinessStillServes is the #165 semantic: a degraded readiness
// check must keep /readyz at 200 (still serving), unlike a down check.
func TestDegradedReadinessStillServes(t *testing.T) {
	t.Parallel()
	reg := newReg()
	reg.Register(statuspage.Check{Name: "cache", Tags: []string{health.TagReadiness}, Fn: func(context.Context) error {
		return statuspage.Degraded("cache disabled")
	}})
	mux := http.NewServeMux()
	health.MountMux(mux, reg)
	if got := code(t, mux, "/readyz"); got != http.StatusOK {
		t.Errorf("degraded /readyz = %d, want 200 (still serving)", got)
	}
}

func TestStartupGate(t *testing.T) {
	t.Parallel()
	reg := newReg()
	gate := health.NewStartupGate()
	reg.Register(gate.Check("startup"))
	mux := http.NewServeMux()
	health.MountMux(mux, reg)

	if got := code(t, mux, "/startupz"); got != http.StatusServiceUnavailable {
		t.Errorf("before MarkComplete: /startupz = %d, want 503", got)
	}
	gate.MarkComplete()
	if !gate.Done() {
		t.Error("Done() should be true after MarkComplete")
	}
	if got := code(t, mux, "/startupz"); got != http.StatusOK {
		t.Errorf("after MarkComplete: /startupz = %d, want 200", got)
	}
}
