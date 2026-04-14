package load_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	vegeta "github.com/tsenart/vegeta/v12/lib"

	"github.com/golusoris/golusoris/testutil/load"
)

func TestAttack_happyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	metrics := load.Attack(t, load.Options{
		Targeter: load.GET(srv.URL + "/"),
		Rate:     vegeta.Rate{Freq: 10, Per: time.Second},
		Duration: 200 * time.Millisecond,
	})

	if metrics.Requests == 0 {
		t.Fatal("expected at least one request")
	}
}

func TestAssert_passesOnGoodMetrics(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	metrics := load.Attack(t, load.Options{
		Targeter: load.GET(srv.URL + "/"),
		Rate:     vegeta.Rate{Freq: 10, Per: time.Second},
		Duration: 200 * time.Millisecond,
	})

	// All responses are 200 OK — checks should pass.
	load.Assert(t, metrics,
		load.MaxErrorRate(0.05),
		load.MaxP99(5*time.Second),
	)
}

func TestCheck_maxErrorRate(t *testing.T) {
	t.Parallel()
	// Build a fake Metrics with 50% success rate.
	m := &vegeta.Metrics{}
	for i := range 10 {
		code := http.StatusOK
		if i%2 == 0 {
			code = http.StatusInternalServerError
		}
		m.Add(&vegeta.Result{Code: uint16(code)}) //nolint:gosec // G115: test code, value bounded
	}
	m.Close()

	check := load.MaxErrorRate(0.01) // 1% max — should fail with ~50% errors
	if msg := check(m); msg == "" {
		t.Fatal("expected failure message, got empty string")
	}
}

func TestConstantRate(t *testing.T) {
	t.Parallel()
	r := load.ConstantRate(100)
	if r.Freq != 100 {
		t.Fatalf("expected Freq=100, got %d", r.Freq)
	}
}
