// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tenancy_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
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
	app := fxtest.New(
		t,
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
	if err := ms.Add(tenancy.Tenant{ID: "t1", Slug: "acme"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

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
	app := fxtest.New(
		t,
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

	// Keep ID distinct from the label to prove the extractor selects slug lookup.
	if err := store.(*tenancy.MemoryStore).Add(tenancy.Tenant{ID: "tenant-42", Slug: "acme"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	var (
		seen bool
		got  tenancy.Tenant
	)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got, seen = tenancy.FromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "acme.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !seen {
		t.Fatal("expected tenant resolved from subdomain")
	}
	if got.ID != "tenant-42" {
		t.Fatalf("tenant ID = %q; want tenant-42", got.ID)
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
	if err := s.Add(tenancy.Tenant{ID: "id1", Slug: "slug1", Plan: "pro"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

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

func TestMemoryStore_UpsertMaintainsUniqueSlugIndex(t *testing.T) {
	t.Parallel()
	s := tenancy.NewMemoryStore()
	if err := s.Add(tenancy.Tenant{ID: "id1", Slug: "Old"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(tenancy.Tenant{ID: "id1", Slug: "New"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindBySlug(context.Background(), "old"); !errors.Is(err, tenancy.ErrTenantNotFound) {
		t.Fatalf("old slug error = %v; want ErrTenantNotFound", err)
	}
	if got, err := s.FindBySlug(context.Background(), "NEW"); err != nil || got.ID != "id1" {
		t.Fatalf("new slug = (%+v, %v); want id1", got, err)
	}
	if err := s.Add(tenancy.Tenant{ID: "id2", Slug: "new"}); !errors.Is(err, tenancy.ErrTenantSlugConflict) {
		t.Fatalf("conflicting Add error = %v; want ErrTenantSlugConflict", err)
	}
}

func TestMemoryStore_AddRejectsEmptyID(t *testing.T) {
	t.Parallel()
	if err := tenancy.NewMemoryStore().Add(tenancy.Tenant{Slug: "orphan"}); !errors.Is(err, tenancy.ErrInvalidTenant) {
		t.Fatalf("Add error = %v; want ErrInvalidTenant", err)
	}
}
