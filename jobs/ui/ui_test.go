package ui_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/jobs/ui"
)

func TestWithBasicAuthNoCredsPassesThrough(t *testing.T) {
	t.Parallel()
	called := false
	h := ui.WithBasicAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}), "", "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Error("handler not called when no creds configured")
	}
}

func TestWithBasicAuthBlocksAnonymous(t *testing.T) {
	t.Parallel()
	h := ui.WithBasicAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("should not reach inner handler")
	}), "admin", "s3cret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("WWW-Authenticate"), "river-ui") {
		t.Errorf("WWW-Authenticate missing realm")
	}
}

func TestWithBasicAuthAllowsCorrectCreds(t *testing.T) {
	t.Parallel()
	called := false
	h := ui.WithBasicAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}), "admin", "s3cret")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:s3cret")))
	h.ServeHTTP(rr, req)
	if !called {
		t.Error("handler not called with valid creds")
	}
}

func TestNewHandlerRequiresClient(t *testing.T) {
	t.Parallel()
	_, err := ui.NewHandler(ui.Options{}, nil)
	if err == nil {
		t.Fatal("expected error for missing Client")
	}
	if !strings.Contains(err.Error(), "Client is required") {
		t.Errorf("err = %v", err)
	}
}
