package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golusoris/golusoris/httpx/ratelimit"
)

func TestEmptyRateIsNoop(t *testing.T) {
	t.Parallel()
	mw, err := ratelimit.New(ratelimit.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for range 100 {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
		if rr.Code != http.StatusTeapot {
			t.Fatalf("status = %d", rr.Code)
		}
	}
}

func TestRateEnforced(t *testing.T) {
	t.Parallel()
	mw, err := ratelimit.New(ratelimit.Options{Rate: "2-S"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	var statuses []int
	for range 4 {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		h.ServeHTTP(rr, req)
		statuses = append(statuses, rr.Code)
	}
	// First two should be 200, last two 429.
	if statuses[0] != 200 || statuses[1] != 200 {
		t.Errorf("first two should pass, got %v", statuses)
	}
	if statuses[2] != http.StatusTooManyRequests || statuses[3] != http.StatusTooManyRequests {
		t.Errorf("third/fourth should be 429, got %v", statuses)
	}
}

func TestBadRateErrors(t *testing.T) {
	t.Parallel()
	_, err := ratelimit.New(ratelimit.Options{Rate: "not-a-rate"})
	if err == nil {
		t.Fatal("expected parse error")
	}
}
