// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ui_test

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/jobs"
	"github.com/golusoris/golusoris/jobs/ui"
)

type typedNilContext struct{ context.Context }

func TestWithBasicAuthNilHandlerFailsClosed(t *testing.T) {
	t.Parallel()

	for _, credentials := range [][2]string{{"", ""}, {"admin", "s3cret"}} {
		var inner http.HandlerFunc
		h := ui.WithBasicAuth(inner, credentials[0], credentials[1])
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if credentials[0] != "" {
			req.SetBasicAuth(credentials[0], credentials[1])
		}
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusInternalServerError {
			t.Errorf("credentials %q/%q returned %d, want 500", credentials[0], credentials[1], rr.Code)
		}
	}
}

func TestStartRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	if cancel, err := ui.Start((*typedNilContext)(nil), nil); err == nil || !strings.Contains(err.Error(), "context") || cancel != nil {
		t.Fatalf("nil context result = (%v, %v), want nil context error", cancel, err)
	}
	if cancel, err := ui.Start(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "handler") || cancel != nil {
		t.Fatalf("nil handler result = (%v, %v), want nil handler error", cancel, err)
	}
}

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

func TestWithBasicAuthPartialCredentialsFailClosed(t *testing.T) {
	t.Parallel()
	for _, credentials := range [][2]string{{"admin", ""}, {"", "s3cret"}} {
		h := ui.WithBasicAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("should not reach inner handler")
		}), credentials[0], credentials[1])
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetBasicAuth(credentials[0], credentials[1])
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("partial credentials returned %d, want 401", rr.Code)
		}
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

func TestNewHandlerRejectsEmbeddedCredentials(t *testing.T) {
	t.Parallel()
	_, err := ui.NewHandler(ui.Options{User: "admin", Password: "s3cret"}, nil)
	if err == nil {
		t.Fatal("expected credentials to be rejected instead of silently ignored")
	}
	if !strings.Contains(err.Error(), "WithBasicAuth") {
		t.Fatalf("err = %v, want WithBasicAuth guidance", err)
	}
}

func TestNewHandlerRejectsRootPrefix(t *testing.T) {
	t.Parallel()

	client, err := jobs.New(nil, jobs.DefaultOptions(), nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	for _, prefix := range []string{"", "/"} {
		_, err := ui.NewHandler(ui.Options{Client: client, Prefix: prefix}, slog.New(slog.DiscardHandler))
		if err == nil || !strings.Contains(err.Error(), "Prefix") {
			t.Errorf("prefix %q error = %v, want required Prefix error", prefix, err)
		}
	}
}
