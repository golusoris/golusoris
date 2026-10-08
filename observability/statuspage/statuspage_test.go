// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package statuspage_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/observability/statuspage"
)

func TestNewRegistryTypedNilClockUsesDefault(t *testing.T) {
	t.Parallel()
	var clk *clockwork.FakeClock
	registry := statuspage.NewRegistry(clk)
	if registry == nil {
		t.Fatal("NewRegistry returned nil")
	}
}

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

func TestDetailsAreIsolatedAcrossProviderHookRunAndCache(t *testing.T) {
	t.Parallel()
	labels := []any{"primary"}
	nested := map[string]any{"labels": labels}
	details := map[string]any{"backend": nested}
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{
		Name:    "db",
		Fn:      func(context.Context) error { return nil },
		Details: func(context.Context) map[string]any { return details },
	})
	r.OnRun(func(_ context.Context, results []statuspage.Result) {
		backend := results[0].Details["backend"].(map[string]any)
		backend["labels"].([]any)[0] = "hook"
	})

	run := r.Run(context.Background())
	labels[0] = "provider"
	backend := run[0].Details["backend"].(map[string]any)
	if got := backend["labels"].([]any)[0]; got != "primary" {
		t.Fatalf("Run details = %v, want primary", got)
	}
	backend["labels"].([]any)[0] = "caller"

	cached := r.Cached()
	cachedBackend := cached[0].Details["backend"].(map[string]any)
	if got := cachedBackend["labels"].([]any)[0]; got != "primary" {
		t.Fatalf("Cached details = %v, want primary", got)
	}
	cachedBackend["labels"].([]any)[0] = "cached caller"
	secondBackend := r.Cached()[0].Details["backend"].(map[string]any)
	if got := secondBackend["labels"].([]any)[0]; got != "primary" {
		t.Fatalf("second Cached details = %v, want primary", got)
	}
}

func TestInvalidDetailsFailClosed(t *testing.T) {
	t.Parallel()
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{
		Name:    "db",
		Fn:      func(context.Context) error { return nil },
		Details: func(context.Context) map[string]any { return map[string]any{"invalid": func() {}} },
	})

	results := r.Run(context.Background())
	if len(results) != 1 || results[0].Status != statuspage.StatusDown {
		t.Fatalf("Run() = %+v, want one down result", results)
	}
	if results[0].Message != statuspage.ErrInvalidDetails.Error() {
		t.Fatalf("message = %q, want %q", results[0].Message, statuspage.ErrInvalidDetails)
	}
	if results[0].Details != nil {
		t.Fatalf("Details = %#v, want nil", results[0].Details)
	}
}

func TestDetailsSnapshotPreservesConcreteTypes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 19, 22, 0, 0, 0, time.UTC)
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{
		Name: "db",
		Fn:   func(context.Context) error { return nil },
		Details: func(context.Context) map[string]any {
			return map[string]any{
				"int64":  int64(-9),
				"uint64": ^uint64(0),
				"bytes":  []byte{1, 2},
				"time":   now,
			}
		},
	})

	details := r.Run(context.Background())[0].Details
	if got := details["int64"]; got != int64(-9) {
		t.Fatalf("int64 = %v (%T)", got, got)
	}
	if got := details["uint64"]; got != ^uint64(0) {
		t.Fatalf("uint64 = %v (%T)", got, got)
	}
	if got := details["bytes"]; string(got.([]byte)) != string([]byte{1, 2}) {
		t.Fatalf("bytes = %v (%T)", got, got)
	}
	if got := details["time"]; got != now {
		t.Fatalf("time = %v (%T)", got, got)
	}
}

func TestNilCheckFailsClosedWithoutPanic(t *testing.T) {
	t.Parallel()
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{Name: "missing"})
	r.OnRun(nil)

	results := r.Run(context.Background())
	if len(results) != 1 {
		t.Fatalf("Run results = %d, want 1", len(results))
	}
	if results[0].Status != statuspage.StatusDown {
		t.Fatalf("status = %q, want down", results[0].Status)
	}
	if results[0].Message != statuspage.ErrNilCheckFunc.Error() {
		t.Fatalf("message = %q, want %q", results[0].Message, statuspage.ErrNilCheckFunc)
	}
}

func TestRegisterClonesTags(t *testing.T) {
	t.Parallel()
	tags := []string{"readiness"}
	r := statuspage.NewRegistry(clock.NewFake())
	r.Register(statuspage.Check{
		Name: "db",
		Tags: tags,
		Fn:   func(context.Context) error { return nil },
	})
	tags[0] = "liveness"
	if got := r.RunTagged(context.Background(), "readiness"); len(got) != 1 {
		t.Fatalf("RunTagged(readiness) results = %d, want 1", len(got))
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
