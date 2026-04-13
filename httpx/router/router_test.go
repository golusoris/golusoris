package router_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/golusoris/golusoris/httpx/router"
)

func TestNewRoutesRequests(t *testing.T) {
	t.Parallel()
	r := router.New()
	r.Get("/hello", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hi"))
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d", rr.Code)
	}
	if rr.Body.String() != "hi" {
		t.Errorf("body = %q", rr.Body.String())
	}
}

// TestNewImplementsChiRouter is a compile-time guard that [router.New]'s
// return type still satisfies chi.Router (the interface apps inject).
func TestNewImplementsChiRouter(t *testing.T) {
	t.Parallel()
	var _ chi.Router = router.New()
}
