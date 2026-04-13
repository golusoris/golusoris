package csrf_test

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/httpx/csrf"
)

func hexKey(t *testing.T) string {
	t.Helper()
	return hex.EncodeToString(make([]byte, 32))
}

func TestNoSecretIsNoop(t *testing.T) {
	t.Parallel()
	mw, err := csrf.New(csrf.Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", nil))
	if rr.Code != http.StatusTeapot {
		t.Errorf("status = %d, want pass-through", rr.Code)
	}
}

func TestPOSTWithoutTokenIsBlocked(t *testing.T) {
	t.Parallel()
	mw, err := csrf.New(csrf.Options{Secret: hexKey(t), Path: "/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", nil))
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestGETIssuesCookie(t *testing.T) {
	t.Parallel()
	mw, err := csrf.New(csrf.Options{Secret: hexKey(t), Path: "/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	cookie := rr.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "_gorilla_csrf") {
		t.Errorf("no CSRF cookie issued: %q", cookie)
	}
}

func TestInvalidSecretErrors(t *testing.T) {
	t.Parallel()
	_, err := csrf.New(csrf.Options{Secret: "too-short"})
	if err == nil {
		t.Fatal("expected error for short secret")
	}
}
