package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEtagRecorder_WriteHeader(t *testing.T) {
	t.Parallel()
	e := &etagRecorder{ResponseWriter: httptest.NewRecorder()}
	e.WriteHeader(201)
	if e.status != 201 {
		t.Errorf("status = %d, want 201", e.status)
	}
}

func TestStatusOrDefault_zero(t *testing.T) {
	t.Parallel()
	if got := statusOrDefault(0); got != http.StatusOK {
		t.Errorf("statusOrDefault(0) = %d, want %d", got, http.StatusOK)
	}
}

func TestStatusOrDefault_nonzero(t *testing.T) {
	t.Parallel()
	if got := statusOrDefault(404); got != 404 {
		t.Errorf("statusOrDefault(404) = %d, want 404", got)
	}
}

func TestStatusRecorder_WriteHeader(t *testing.T) {
	t.Parallel()
	s := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	s.WriteHeader(201)
	if s.status != 201 {
		t.Errorf("status = %d, want 201", s.status)
	}
}
