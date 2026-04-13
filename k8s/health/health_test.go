package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

func mountHelper(t *testing.T) (chi.Router, *statuspage.Registry) {
	t.Helper()
	r := chi.NewRouter()
	reg := statuspage.NewRegistry(clock.NewFake())
	health.Mount(r, reg)
	return r, reg
}

func TestLivezOnlyRunsLivenessChecks(t *testing.T) {
	t.Parallel()
	r, reg := mountHelper(t)

	var liveCalled, readyCalled bool
	reg.Register(statuspage.Check{
		Name: "liveness-check",
		Tags: []string{health.TagLiveness},
		Fn:   func(context.Context) error { liveCalled = true; return nil },
	})
	reg.Register(statuspage.Check{
		Name: "readiness-check",
		Tags: []string{health.TagReadiness},
		Fn:   func(context.Context) error { readyCalled = true; return errors.New("db down") },
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/livez", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d", rr.Code)
	}
	if !liveCalled {
		t.Error("liveness check not invoked")
	}
	if readyCalled {
		t.Error("readiness check should not run on /livez")
	}
}

func TestReadyzReturns503OnDown(t *testing.T) {
	t.Parallel()
	r, reg := mountHelper(t)
	reg.Register(statuspage.Check{
		Name: "db",
		Tags: []string{health.TagReadiness},
		Fn:   func(context.Context) error { return errors.New("connection refused") },
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not ok") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestVerboseReturnsJSON(t *testing.T) {
	t.Parallel()
	r, reg := mountHelper(t)
	reg.Register(statuspage.Check{
		Name: "db",
		Tags: []string{health.TagReadiness},
		Fn:   func(context.Context) error { return nil },
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz?verbose=1", nil))

	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var resp struct {
		Status string
		Tag    string
		Checks []struct {
			Name   string
			Status string
		}
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "up" || resp.Tag != health.TagReadiness {
		t.Errorf("resp = %+v", resp)
	}
	if len(resp.Checks) != 1 || resp.Checks[0].Name != "db" {
		t.Errorf("checks = %+v", resp.Checks)
	}
}

func TestStartupzFiltersBytag(t *testing.T) {
	t.Parallel()
	r, reg := mountHelper(t)
	reg.Register(statuspage.Check{
		Name: "migrations",
		Tags: []string{health.TagStartup},
		Fn:   func(context.Context) error { return nil },
	})
	// A non-startup check should be ignored.
	reg.Register(statuspage.Check{
		Name: "extra",
		Tags: []string{health.TagReadiness},
		Fn:   func(context.Context) error { return errors.New("ignore me") },
	})

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/startupz?verbose=1", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d", rr.Code)
	}
	var resp struct {
		Checks []struct{ Name string }
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Checks) != 1 || resp.Checks[0].Name != "migrations" {
		t.Errorf("checks = %+v", resp.Checks)
	}
}
