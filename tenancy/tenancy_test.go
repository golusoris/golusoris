package tenancy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golusoris/golusoris/tenancy"
)

// stubStore resolves a fixed set of tenants.
type stubStore struct {
	byID   map[string]tenancy.Tenant
	bySlug map[string]tenancy.Tenant
}

func (s *stubStore) FindByID(_ context.Context, id string) (tenancy.Tenant, error) {
	t, ok := s.byID[id]
	if !ok {
		return tenancy.Tenant{}, errors.New("not found")
	}
	return t, nil
}
func (s *stubStore) FindBySlug(_ context.Context, slug string) (tenancy.Tenant, error) {
	t, ok := s.bySlug[slug]
	if !ok {
		return tenancy.Tenant{}, errors.New("not found")
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
	acme := tenancy.Tenant{ID: "t1", Slug: "acme", Plan: "pro"}
	store := newStore(acme)
	extract := tenancy.HeaderExtractor("X-Tenant-ID")
	handler := tenancy.Middleware(extract, store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ten, ok := tenancy.FromContext(r.Context())
		if !ok {
			http.Error(w, "no tenant", http.StatusInternalServerError)
			return
		}
		w.Write([]byte(ten.Slug)) //nolint:errcheck
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "t1")
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
	var reached bool
	handler := tenancy.Middleware(
		func(_ *http.Request) (string, error) { return "", tenancy.ErrNoTenant },
		newStore(),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if !reached {
		t.Fatal("handler should be called when ErrNoTenant")
	}
}

func TestMiddleware_unknownTenant(t *testing.T) {
	extract := tenancy.HeaderExtractor("X-Tenant-ID")
	handler := tenancy.Middleware(extract, newStore())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-ID", "ghost")
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	if rw.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rw.Code)
	}
}

func TestSubdomainExtractor(t *testing.T) {
	ext := tenancy.SubdomainExtractor("example.com")

	cases := []struct {
		host    string
		wantID  string
		wantErr bool
	}{
		{"acme.example.com", "acme", false},
		{"example.com", "", true},
		{"www.example.com", "", true},
		{"other.io", "", true},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = c.host
		id, err := ext(req)
		if c.wantErr && err == nil {
			t.Errorf("host %s: expected error", c.host)
		}
		if !c.wantErr && id != c.wantID {
			t.Errorf("host %s: expected %q, got %q", c.host, c.wantID, id)
		}
	}
}

func TestMustFromContext_panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	tenancy.MustFromContext(context.Background())
}
