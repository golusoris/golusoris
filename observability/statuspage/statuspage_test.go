package statuspage_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/observability/statuspage"
)

func TestAllUpRendersJSON(t *testing.T) {
	t.Parallel()
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{
		Name: "db",
		Fn:   func(context.Context) error { return nil },
	})

	req := httptest.NewRequest(http.MethodGet, "/?format=json", nil)
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp struct {
		Status string
		Checks []struct {
			Name   string
			Status string
		}
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "up" {
		t.Errorf("status = %q", resp.Status)
	}
	if len(resp.Checks) != 1 || resp.Checks[0].Name != "db" || resp.Checks[0].Status != "up" {
		t.Errorf("checks = %+v", resp.Checks)
	}
}

func TestAnyDownReturns503(t *testing.T) {
	t.Parallel()
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{Name: "ok", Fn: func(context.Context) error { return nil }})
	r.Register(statuspage.Check{Name: "broken", Fn: func(context.Context) error { return errors.New("boom") }})

	req := httptest.NewRequest(http.MethodGet, "/?format=json", nil)
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestHasTag(t *testing.T) {
	t.Parallel()
	c := statuspage.Check{Name: "x", Tags: []string{"liveness", "readiness"}}
	if !c.HasTag("liveness") {
		t.Error("expected HasTag(liveness) = true")
	}
	if c.HasTag("startup") {
		t.Error("expected HasTag(startup) = false")
	}
}

func TestRunTagged(t *testing.T) {
	t.Parallel()
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{
		Name: "live",
		Tags: []string{"liveness"},
		Fn:   func(context.Context) error { return nil },
	})
	r.Register(statuspage.Check{
		Name: "ready",
		Tags: []string{"readiness"},
		Fn:   func(context.Context) error { return nil },
	})
	results := r.RunTagged(context.Background(), "liveness")
	if len(results) != 1 || results[0].Name != "live" {
		t.Errorf("RunTagged(liveness) = %+v", results)
	}
}

func TestOnRunHook(t *testing.T) {
	t.Parallel()
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{Name: "x", Fn: func(context.Context) error { return nil }})
	var called int
	r.OnRun(func(_ context.Context, _ []statuspage.Result) { called++ })
	r.Run(context.Background())
	if called != 1 {
		t.Errorf("hook called %d times, want 1", called)
	}
}

func TestCached(t *testing.T) {
	t.Parallel()
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{Name: "db", Fn: func(context.Context) error { return nil }})
	// Before Run, Cached returns empty.
	if got := r.Cached(); len(got) != 0 {
		t.Errorf("before Run, Cached = %+v", got)
	}
	r.Run(context.Background())
	got := r.Cached()
	if len(got) != 1 || got[0].Name != "db" {
		t.Errorf("after Run, Cached = %+v", got)
	}
}

func TestHTMLOutput(t *testing.T) {
	t.Parallel()
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{Name: "db", Fn: func(context.Context) error { return nil }})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rr := httptest.NewRecorder()
	r.Handler().ServeHTTP(rr, req)

	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rr.Body.String(), "System: up") {
		t.Errorf("body missing status header: %q", rr.Body.String())
	}
}
