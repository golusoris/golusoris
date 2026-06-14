package tenancy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/middleware"
)

// Module wires the tenancy middleware into the fx graph. It provides a
// [middleware.Middleware] (the tenant-resolving HTTP middleware) and a [Store]
// (defaulting to an in-memory [MemoryStore]). Apps attach the middleware to
// their router stack and replace the store with a real one via fx.Decorate.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    tenancy.Module, // provides middleware.Middleware + tenancy.Store
//	    fx.Decorate(func(*tenancy.MemoryStore) tenancy.Store { return myPostgresStore }),
//	    fx.Invoke(func(r *router.Router, mw middleware.Middleware) { r.Use(mw) }),
//	)
//
// Config key prefix: tenancy.*
//
//	tenancy.extractor    # "header" (default) or "subdomain"
//	tenancy.header       # header name when extractor=header (default "X-Tenant-ID")
//	tenancy.base_domain  # base domain when extractor=subdomain (e.g. "example.com")
var Module = fx.Module("golusoris.tenancy",
	fx.Provide(loadOptions),
	fx.Provide(newMemoryStore),
	fx.Provide(newExtractor),
	fx.Provide(newMiddleware),
)

// Options configures how the tenancy middleware resolves tenants.
type Options struct {
	// Extractor selects the built-in extractor: "header" (default) reads the
	// tenant ID from a request header; "subdomain" derives it from the host's
	// first label.
	Extractor string `koanf:"extractor"`
	// Header is the header name read when Extractor is "header"
	// (default "X-Tenant-ID").
	Header string `koanf:"header"`
	// BaseDomain is the domain stripped when Extractor is "subdomain"
	// (e.g. "example.com"). Required for the subdomain extractor.
	BaseDomain string `koanf:"base_domain"`
}

func defaultOptions() Options {
	return Options{Extractor: "header", Header: "X-Tenant-ID"}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("tenancy", &opts); err != nil {
		return Options{}, fmt.Errorf("tenancy: load options: %w", err)
	}
	return opts, nil
}

// newExtractor builds the configured [ExtractFunc]. Unknown extractor names or
// a missing base domain for the subdomain extractor are configuration errors.
func newExtractor(opts Options) (ExtractFunc, error) {
	switch opts.Extractor {
	case "header":
		return HeaderExtractor(opts.Header), nil
	case "subdomain":
		if opts.BaseDomain == "" {
			return nil, errors.New("tenancy: subdomain extractor needs tenancy.base_domain")
		}
		return SubdomainExtractor(opts.BaseDomain), nil
	default:
		return nil, fmt.Errorf("tenancy: unknown extractor %q (want \"header\" or \"subdomain\")", opts.Extractor)
	}
}

// newMiddleware assembles the tenant-resolving middleware from the extractor
// and store. It is exposed as the canonical [middleware.Middleware] type so it
// composes with the router stack.
func newMiddleware(extract ExtractFunc, store Store, logger *slog.Logger) middleware.Middleware {
	logger.Debug("tenancy: middleware ready")
	return Middleware(extract, store)
}

// MemoryStore is an in-memory [Store] keyed by tenant ID and slug. It is the
// default store wired by [Module] so the framework boots without a database;
// apps replace it with a real (e.g. Postgres-backed) store via fx.Decorate.
type MemoryStore struct {
	mu     sync.RWMutex
	byID   map[string]Tenant
	bySlug map[string]Tenant
}

// newMemoryStore returns an empty [MemoryStore] as the default [Store].
func newMemoryStore() Store {
	return NewMemoryStore()
}

// NewMemoryStore returns an empty [MemoryStore].
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byID: map[string]Tenant{}, bySlug: map[string]Tenant{}}
}

// Add upserts a tenant, indexing it by both ID and slug.
func (s *MemoryStore) Add(t Tenant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[t.ID] = t
	if t.Slug != "" {
		s.bySlug[t.Slug] = t
	}
}

// ErrTenantNotFound is returned by [MemoryStore] when no tenant matches.
var ErrTenantNotFound = errors.New("tenancy: tenant not found")

// FindByID implements [Store].
func (s *MemoryStore) FindByID(_ context.Context, id string) (Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.byID[id]
	if !ok {
		return Tenant{}, ErrTenantNotFound
	}
	return t, nil
}

// FindBySlug implements [Store].
func (s *MemoryStore) FindBySlug(_ context.Context, slug string) (Tenant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.bySlug[slug]
	if !ok {
		return Tenant{}, ErrTenantNotFound
	}
	return t, nil
}
