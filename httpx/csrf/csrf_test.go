package csrf_test

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
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

// TestCrossOriginPOSTIsBlocked verifies the middleware rejects a POST
// whose Sec-Fetch-Site explicitly marks it as cross-site. This is the
// primary contract of filippo.io/csrf/gorilla (same-origin enforcement).
func TestCrossOriginPOSTIsBlocked(t *testing.T) {
	t.Parallel()
	mw, err := csrf.New(csrf.Options{Secret: hexKey(t), Path: "/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for cross-site POST", rr.Code)
	}
}

// TestNonBrowserPOSTPassesThrough: requests with no Sec-Fetch-Site
// and no Origin header are treated as non-browser (curl, server-to-server)
// and allowed by design — CSRF is a browser-context attack.
func TestNonBrowserPOSTPassesThrough(t *testing.T) {
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
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for non-browser POST", rr.Code)
	}
}

// TestSameOriginPOSTPasses: Sec-Fetch-Site=same-origin is allowed.
func TestSameOriginPOSTPasses(t *testing.T) {
	t.Parallel()
	mw, err := csrf.New(csrf.Options{Secret: hexKey(t), Path: "/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for same-origin POST", rr.Code)
	}
}

func TestInvalidSecretErrors(t *testing.T) {
	t.Parallel()
	_, err := csrf.New(csrf.Options{Secret: "too-short"})
	if err == nil {
		t.Fatal("expected error for short secret")
	}
}

// TestToken_returnsString: Token() is retained for template compatibility
// but its value is ignored by the same-origin enforcement.
func TestToken_returnsString(t *testing.T) {
	t.Parallel()
	mw, err := csrf.New(csrf.Options{Secret: hexKey(t), Path: "/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = csrf.Token(r) // must not panic
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}
