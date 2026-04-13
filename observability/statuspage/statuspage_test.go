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
