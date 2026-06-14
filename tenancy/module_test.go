package tenancy_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/middleware"
	"github.com/golusoris/golusoris/tenancy"
)

func newConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfg
}

// TestLoadOptions_Defaults asserts loadOptions yields the header extractor
// defaults on empty config (exercised through the booting Module).
func TestModule_DefaultsHeaderExtractor(t *testing.T) {
	t.Parallel()

	var mw middleware.Middleware
	var store tenancy.Store
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return newConfig(t) }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		tenancy.Module,
		fx.Populate(&mw, &store),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = app.Stop(ctx) })

	if mw == nil {
		t.Fatal("expected middleware to be provided")
	}
	if store == nil {
		t.Fatal("expected store to be provided")
	}

	// The default MemoryStore is provided; seed it and resolve via the
	// default header extractor ("X-Tenant-ID").
	ms, ok := store.(*tenancy.MemoryStore)
	if !ok {
		t.Fatalf("default store is %T, want *tenancy.MemoryStore", store)
	}
	ms.Add(tenancy.Tenant{ID: "t1", Slug: "acme"})

	var got tenancy.Tenant
	var seen bool
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, seen = tenancy.FromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Tenant-Id", "t1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !seen || got.ID != "t1" {
		t.Fatalf("tenant from context = %+v (seen=%v), want ID t1", got, seen)
	}
}

// TestModule_SubdomainExtractor boots the Module with the subdomain extractor
// configured via config and resolves a tenant from the host.
func TestModule_SubdomainExtractor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := "tenancy:\n  extractor: subdomain\n  base_domain: example.com\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.New(config.Options{Files: []string{path}, Watch: false})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	var mw middleware.Middleware
	var store tenancy.Store
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		tenancy.Module,
		fx.Populate(&mw, &store),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = app.Stop(ctx) })

	// Subdomain extractor resolves by ID using the slug label; seed an
	// id matching the subdomain label.
	store.(*tenancy.MemoryStore).Add(tenancy.Tenant{ID: "acme", Slug: "acme"})

	var seen bool
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, seen = tenancy.FromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "acme.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !seen {
		t.Fatal("expected tenant resolved from subdomain")
	}
}

func TestMemoryStore_NotFound(t *testing.T) {
	t.Parallel()
	s := tenancy.NewMemoryStore()
	if _, err := s.FindByID(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for missing tenant")
	}
	if _, err := s.FindBySlug(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for missing slug")
	}
}

func TestMemoryStore_RoundTrip(t *testing.T) {
	t.Parallel()
	s := tenancy.NewMemoryStore()
	s.Add(tenancy.Tenant{ID: "id1", Slug: "slug1", Plan: "pro"})

	byID, err := s.FindByID(context.Background(), "id1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if byID.Plan != "pro" {
		t.Errorf("plan = %q, want pro", byID.Plan)
	}
	bySlug, err := s.FindBySlug(context.Background(), "slug1")
	if err != nil {
		t.Fatalf("FindBySlug: %v", err)
	}
	if bySlug.ID != "id1" {
		t.Errorf("id = %q, want id1", bySlug.ID)
	}
}
