// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tenancy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/tenancy"
)

// stubStore resolves a fixed set of tenants.
type stubStore struct {
	byID   map[string]tenancy.Tenant
	bySlug map[string]tenancy.Tenant
}

type typedNilStore struct{}

type failingStore struct{ err error }

func (s failingStore) FindByID(context.Context, string) (tenancy.Tenant, error) {
	return tenancy.Tenant{}, s.err
}

func (s failingStore) FindBySlug(context.Context, string) (tenancy.Tenant, error) {
	return tenancy.Tenant{}, s.err
}

func (s *typedNilStore) FindByID(context.Context, string) (tenancy.Tenant, error) {
	if s == nil {
		panic("typed-nil store dereferenced")
	}
	return tenancy.Tenant{}, nil
}

func (s *typedNilStore) FindBySlug(context.Context, string) (tenancy.Tenant, error) {
	if s == nil {
		panic("typed-nil store dereferenced")
	}
	return tenancy.Tenant{}, nil
}

func (s *stubStore) FindByID(_ context.Context, id string) (tenancy.Tenant, error) {
	t, ok := s.byID[id]
	if !ok {
		return tenancy.Tenant{}, tenancy.ErrTenantNotFound
	}
	return t, nil
}

func (s *stubStore) FindBySlug(_ context.Context, slug string) (tenancy.Tenant, error) {
	t, ok := s.bySlug[slug]
	if !ok {
		return tenancy.Tenant{}, tenancy.ErrTenantNotFound
	}
	return t, nil
}

func newStore(tenants ...tenancy.Tenant) *stubStore {
	s := &stubStore{
		byID:   make(map[string]tenancy.Tenant),
		bySlug: make(map[string]tenancy.Tenant),
	}
	for _, t := range tenants {
		s.byID[t.ID] = t
		s.bySlug[t.Slug] = t
	}
	return s
}

func TestMiddleware_header(t *testing.T) {
	t.Parallel()
	acme := tenancy.Tenant{ID: "t1", Slug: "acme", Plan: "pro"}
	store := newStore(acme)
	extract := tenancy.HeaderExtractor("X-Tenant-ID")
	handler := tenancy.Middleware(extract, store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ten, ok := tenancy.FromContext(r.Context())
		if !ok {
			http.Error(w, "no tenant", http.StatusInternalServerError)
			return
		}
		w.Write([]byte(ten.Slug))
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-Id", "t1")
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body)
	}
	if rw.Body.String() != "acme" {
		t.Fatalf("expected 'acme', got %q", rw.Body.String())
	}
}

func TestMiddleware_noTenant(t *testing.T) {
	t.Parallel()
	tests := map[string]tenancy.ExtractFunc{
		"sentinel": func(_ *http.Request) (tenancy.TenantRef, error) {
			return tenancy.TenantRef{}, tenancy.ErrNoTenant
		},
		"empty reference": func(_ *http.Request) (tenancy.TenantRef, error) {
			return tenancy.TenantRef{}, nil
		},
	}
	for name, extract := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var reached bool
			handler := tenancy.Middleware(extract, newStore())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			if !reached {
				t.Fatal("handler should be called when extractor reports no tenant")
			}
		})
	}
}

func TestMiddleware_unknownTenant(t *testing.T) {
	t.Parallel()
	extract := tenancy.HeaderExtractor("X-Tenant-ID")
	handler := tenancy.Middleware(extract, newStore())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-Id", "ghost")
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestMiddleware_NilDependenciesFailClosed(t *testing.T) {
	t.Parallel()
	var typedNil *typedNilStore
	tests := map[string]struct {
		extract tenancy.ExtractFunc
		store   tenancy.Store
	}{
		"nil extractor": {store: newStore()},
		"typed-nil store": {
			extract: func(*http.Request) (tenancy.TenantRef, error) {
				return tenancy.TenantRef{Kind: tenancy.TenantRefByID, Value: "tenant"}, nil
			},
			store: typedNil,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			handler := tenancy.Middleware(tc.extract, tc.store)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("next called with an invalid middleware dependency")
			}))
			rw := httptest.NewRecorder()
			handler.ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/", nil))
			if rw.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d; want %d", rw.Code, http.StatusInternalServerError)
			}
		})
	}
}

func TestMiddleware_SubdomainResolvesBySlug(t *testing.T) {
	t.Parallel()
	store := newStore(tenancy.Tenant{ID: "tenant-42", Slug: "acme"})
	handler := tenancy.Middleware(tenancy.SubdomainExtractor("example.com"), store)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			tenant, ok := tenancy.FromContext(r.Context())
			if !ok || tenant.ID != "tenant-42" {
				t.Fatalf("tenant = %+v, present = %v; want slug-resolved tenant", tenant, ok)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "acme.example.com"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestMiddleware_ErrorsAreClassifiedWithoutLeakage(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		extract tenancy.ExtractFunc
		store   tenancy.Store
		status  int
	}{
		"extractor": {
			extract: func(*http.Request) (tenancy.TenantRef, error) {
				return tenancy.TenantRef{}, errors.New("extract-secret")
			},
			store:  newStore(),
			status: http.StatusBadRequest,
		},
		"unknown tenant": {
			extract: tenancy.HeaderExtractor("X-Tenant-ID"),
			store:   newStore(),
			status:  http.StatusUnauthorized,
		},
		"store failure": {
			extract: tenancy.HeaderExtractor("X-Tenant-ID"),
			store:   failingStore{err: errors.New("database-secret")},
			status:  http.StatusInternalServerError,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			handler := tenancy.Middleware(tc.extract, tc.store)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("next called after tenant resolution error")
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("X-Tenant-Id", "missing")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.status {
				t.Fatalf("status = %d; want %d", recorder.Code, tc.status)
			}
			if body := recorder.Body.String(); strings.Contains(body, "secret") {
				t.Fatalf("response leaked internal error: %q", body)
			}
		})
	}
}

func TestMiddleware_NilNextFailsClosed(t *testing.T) {
	t.Parallel()
	handler := tenancy.Middleware(tenancy.HeaderExtractor("X-Tenant-ID"), newStore())(nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want %d", recorder.Code, http.StatusInternalServerError)
	}
}

func TestMiddleware_ContextMetadataIsIsolated(t *testing.T) {
	t.Parallel()
	store := newStore(tenancy.Tenant{
		ID:       "tenant-42",
		Metadata: map[string]any{"region": "original"},
	})
	handler := tenancy.Middleware(tenancy.HeaderExtractor("X-Tenant-ID"), store)(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			first, ok := tenancy.FromContext(r.Context())
			if !ok {
				t.Fatal("tenant missing from context")
			}
			first.Metadata["region"] = "mutated"
			second, _ := tenancy.FromContext(r.Context())
			if got := second.Metadata["region"]; got != "original" {
				t.Fatalf("second context read metadata = %v; want original", got)
			}
		},
	))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-Id", "tenant-42")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	stored, err := store.FindByID(context.Background(), "tenant-42")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got := stored.Metadata["region"]; got != "original" {
		t.Fatalf("stored metadata = %v; want original", got)
	}
}

func TestSubdomainExtractor(t *testing.T) {
	t.Parallel()
	ext := tenancy.SubdomainExtractor("example.com")

	cases := []struct {
		host    string
		wantID  string
		wantErr bool
	}{
		{"acme.example.com", "acme", false},
		{"ACME.EXAMPLE.COM:443", "acme", false},
		{"acme.example.com.", "acme", false},
		{"nested.acme.example.com", "", true},
		{"example.com", "", true},
		{"www.example.com", "", true},
		{"other.io", "", true},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = c.host
		ref, err := ext(req)
		if c.wantErr && err == nil {
			t.Errorf("host %s: expected error", c.host)
		}
		if !c.wantErr && (ref.Kind != tenancy.TenantRefBySlug || ref.Value != c.wantID) {
			t.Errorf("host %s: expected slug %q, got %+v", c.host, c.wantID, ref)
		}
	}
}

func TestRequireFromContext_present(t *testing.T) {
	t.Parallel()
	acme := tenancy.Tenant{ID: "t1", Slug: "acme", Plan: "pro"}
	var (
		got    tenancy.Tenant
		gotErr error
	)
	handler := tenancy.Middleware(tenancy.HeaderExtractor("X-Tenant-ID"), newStore(acme))(
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got, gotErr = tenancy.RequireFromContext(r.Context())
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-Id", "t1")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if gotErr != nil {
		t.Fatalf("RequireFromContext behind Middleware: %v", gotErr)
	}
	if got.ID != acme.ID || got.Slug != acme.Slug || got.Plan != acme.Plan {
		t.Fatalf("tenant = %+v, want %+v", got, acme)
	}
}

func TestRequireFromContext_missing(t *testing.T) {
	t.Parallel()
	_, err := tenancy.RequireFromContext(context.Background())
	if !errors.Is(err, tenancy.ErrMissingTenant) {
		t.Fatalf("expected ErrMissingTenant, got %v", err)
	}
}
