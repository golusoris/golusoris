package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEtagRecorder_WriteHeader(t *testing.T) {
	t.Parallel()
	e := &etagRecorder{ResponseWriter: httptest.NewRecorder()}
	e.WriteHeader(http.StatusCreated)
	if e.status != http.StatusCreated {
		t.Errorf("status = %d, want %d", e.status, http.StatusCreated)
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
	s.WriteHeader(http.StatusCreated)
	if s.status != http.StatusCreated {
		t.Errorf("status = %d, want %d", s.status, http.StatusCreated)
	}
}
