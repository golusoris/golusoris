package cors_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golusoris/golusoris/httpx/cors"
)

func TestPreflightAllowsConfiguredOrigin(t *testing.T) {
	t.Parallel()
	mw := cors.New(cors.Options{
		Origins: []string{"https://app.example"},
		Methods: []string{http.MethodGet, http.MethodPost},
		Headers: []string{"Content-Type"},
	})
	h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("Allow-Origin = %q", got)
	}
}

func TestBlocksUnlistedOrigin(t *testing.T) {
	t.Parallel()
	mw := cors.New(cors.Options{Origins: []string{"https://app.example"}})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Request passes through (handler runs), but Allow-Origin is not set.
	if rr.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("Allow-Origin should be empty for disallowed origin")
	}
}
