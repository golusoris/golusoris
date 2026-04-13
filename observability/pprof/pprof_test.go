package pprof_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golusoris/golusoris/observability/pprof"
)

func TestNoAuthAllowsIndex(t *testing.T) {
	t.Parallel()
	h := pprof.Handler(pprof.Options{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestAuthRequiredWhenSet(t *testing.T) {
	t.Parallel()
	h := pprof.Handler(pprof.Options{User: "admin", Password: "s3cret"})

	// No auth header -> 401.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no-auth status = %d", rr.Code)
	}

	// Correct creds -> 200.
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	auth := base64.StdEncoding.EncodeToString([]byte("admin:s3cret"))
	req.Header.Set("Authorization", "Basic "+auth)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("authed status = %d", rr.Code)
	}

	// Wrong password -> 401.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	auth = base64.StdEncoding.EncodeToString([]byte("admin:wrong"))
	req.Header.Set("Authorization", "Basic "+auth)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong-pass status = %d", rr.Code)
	}
}
