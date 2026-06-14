package extclient_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/httpx/extclient"
)

type user struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestNewValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{name: "empty", baseURL: "", wantErr: true},
		{name: "relative", baseURL: "/foo", wantErr: true},
		{name: "no scheme", baseURL: "api.example.com", wantErr: true},
		{name: "absolute", baseURL: "https://api.example.com", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := extclient.New(extclient.ServiceOptions{BaseURL: tt.baseURL})
			if (err != nil) != tt.wantErr {
				t.Fatalf("New(%q) err = %v, wantErr = %v", tt.baseURL, err, tt.wantErr)
			}
		})
	}
}

func TestGetDecodesJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/42" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		_ = json.NewEncoder(w).Encode(user{ID: "42", Name: "ada"})
	}))
	defer srv.Close()

	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := extclient.Get[user](context.Background(), c, "/users/42", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != (user{ID: "42", Name: "ada"}) {
		t.Errorf("got = %+v", got)
	}
}

func TestAuthAndHeaders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		opts       extclient.ServiceOptions
		perRequest map[string]string
		wantAuth   string
		wantHeader map[string]string
	}{
		{
			name:     "bearer",
			opts:     extclient.ServiceOptions{Bearer: "tok123"},
			wantAuth: "Bearer tok123",
		},
		{
			name:       "api key header",
			opts:       extclient.ServiceOptions{AuthHeader: map[string]string{"X-API-Key": "secret"}},
			wantHeader: map[string]string{"X-Api-Key": "secret"},
		},
		{
			name: "bearer wins over auth header Authorization",
			opts: extclient.ServiceOptions{
				Bearer:     "tok123",
				AuthHeader: map[string]string{"Authorization": "Basic nope"},
			},
			wantAuth: "Bearer tok123",
		},
		{
			name:       "per-request overrides default",
			opts:       extclient.ServiceOptions{Headers: map[string]string{"X-Trace": "default"}},
			perRequest: map[string]string{"X-Trace": "override"},
			wantHeader: map[string]string{"X-Trace": "override"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.wantAuth != "" && r.Header.Get("Authorization") != tt.wantAuth {
					t.Errorf("Authorization = %q, want %q", r.Header.Get("Authorization"), tt.wantAuth)
				}
				for k, v := range tt.wantHeader {
					if got := r.Header.Get(k); got != v {
						t.Errorf("header %s = %q, want %q", k, got, v)
					}
				}
				_ = json.NewEncoder(w).Encode(user{ID: "1"})
			}))
			defer srv.Close()

			opts := tt.opts
			opts.BaseURL = srv.URL
			c, err := extclient.New(opts)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := extclient.Get[user](context.Background(), c, "/x", tt.perRequest); err != nil {
				t.Fatalf("Get: %v", err)
			}
		})
	}
}

func TestPostSendsBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var in user
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("decode: %v", err)
		}
		in.ID = "created"
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(in)
	}))
	defer srv.Close()

	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := extclient.Post[user](context.Background(), c, "/users", user{Name: "grace"}, nil)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got.ID != "created" || got.Name != "grace" {
		t.Errorf("got = %+v", got)
	}
}

func TestNon2xxReturnsAPIError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, `{"detail":"nope"}`)
	}))
	defer srv.Close()

	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = extclient.Get[user](context.Background(), c, "/x", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, extclient.ErrStatus) {
		t.Errorf("errors.Is(ErrStatus) = false: %v", err)
	}
	var apiErr *extclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(*APIError) = false: %v", err)
	}
	if apiErr.Status != http.StatusTeapot {
		t.Errorf("Status = %d", apiErr.Status)
	}
	if apiErr.Body != `{"detail":"nope"}` {
		t.Errorf("Body = %q", apiErr.Body)
	}
}

func TestDeleteEmptyBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 204 with empty body decodes to the zero value without error.
	got, err := extclient.Delete[user](context.Background(), c, "/users/1", nil)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got != (user{}) {
		t.Errorf("got = %+v, want zero", got)
	}
}

func TestGetCachesByURL(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(user{ID: "cached"})
	}))
	defer srv.Close()

	pool, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}
	c, err := extclient.New(
		extclient.ServiceOptions{BaseURL: srv.URL, CacheTTL: time.Minute},
		extclient.WithCache(pool),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for range 3 {
		got, gErr := extclient.Get[user](context.Background(), c, "/thing", nil)
		if gErr != nil {
			t.Fatalf("Get: %v", gErr)
		}
		if got.ID != "cached" {
			t.Errorf("got = %+v", got)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("upstream hits = %d, want 1 (cache should serve the rest)", n)
	}
}

func TestNoCacheWithoutPool(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(user{ID: "x"})
	}))
	defer srv.Close()

	// CacheTTL set but no pool attached -> caching silently disabled.
	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL, CacheTTL: time.Minute})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for range 2 {
		if _, gErr := extclient.Get[user](context.Background(), c, "/y", nil); gErr != nil {
			t.Fatalf("Get: %v", gErr)
		}
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("upstream hits = %d, want 2 (no caching without pool)", n)
	}
}

func TestRegistryLookup(t *testing.T) {
	t.Parallel()
	opts := extclient.Options{
		Services: map[string]extclient.ServiceOptions{
			"github": {BaseURL: "https://api.github.com"},
			"stripe": {BaseURL: "https://api.stripe.com"},
		},
	}
	reg, err := extclient.NewRegistryForTest(opts, discardLogger(), nil)
	if err != nil {
		t.Fatalf("NewRegistryForTest: %v", err)
	}
	if _, err := reg.Client("github"); err != nil {
		t.Errorf("Client(github): %v", err)
	}
	if _, err := reg.Client("missing"); err == nil {
		t.Error("Client(missing) = nil err, want error")
	}
	names := reg.Names()
	if len(names) != 2 || names[0] != "github" || names[1] != "stripe" {
		t.Errorf("Names() = %v", names)
	}
}

func TestRegistryRejectsBadService(t *testing.T) {
	t.Parallel()
	opts := extclient.Options{
		Services: map[string]extclient.ServiceOptions{
			"broken": {BaseURL: ""},
		},
	}
	if _, err := extclient.NewRegistryForTest(opts, discardLogger(), nil); err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
}
