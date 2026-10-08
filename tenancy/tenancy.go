// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package tenancy provides multi-tenant context propagation and HTTP middleware.
// A Tenant is resolved from the incoming request (subdomain, header, path
// segment, JWT claim, …) and stored in the context so downstream handlers
// and service functions can call [FromContext] without threading IDs manually.
//
// Usage:
//
//	extractor := tenancy.SubdomainExtractor("example.com")
//	mux.Use(tenancy.Middleware(extractor, store))
//
//	// In a handler:
//	t, ok := tenancy.FromContext(r.Context())
package tenancy

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"strings"

	"github.com/golusoris/golusoris/core/validate"
)

// Tenant represents a single tenant in a multi-tenant application.
type Tenant struct {
	ID   string
	Slug string
	Plan string
	// Metadata holds app-specific fields (custom domains, feature flags, …).
	Metadata map[string]any
}

// Store resolves a tenant by ID or slug. Missing records return an error that
// wraps [ErrTenantNotFound]; operational failures return a different error.
type Store interface {
	FindByID(ctx context.Context, id string) (Tenant, error)
	FindBySlug(ctx context.Context, slug string) (Tenant, error)
}

// TenantRefKind selects the Store lookup used for a [TenantRef].
type TenantRefKind uint8

const (
	// TenantRefByID resolves a reference with [Store.FindByID].
	TenantRefByID TenantRefKind = iota + 1
	// TenantRefBySlug resolves a reference with [Store.FindBySlug].
	TenantRefBySlug
)

// TenantRef identifies a tenant and declares how it must be resolved.
type TenantRef struct {
	Kind  TenantRefKind
	Value string
}

// ExtractFunc extracts a typed tenant reference from an incoming request.
// Return (TenantRef{}, nil) to signal "no tenant" (e.g. landing pages).
// Return a non-nil error to reject the request (HTTP 400/404).
type ExtractFunc func(r *http.Request) (ref TenantRef, err error)

// ErrNoTenant is returned by [ExtractFunc] to signal the request is not
// tenant-scoped (e.g. landing page). The middleware passes through without
// setting context.
var ErrNoTenant = errors.New("tenancy: no tenant")

type contextKey struct{}

// Middleware resolves the tenant for each request and stores it in the context.
// Unknown tenants return 401; store failures return 500.
// If extract returns [ErrNoTenant] the middleware passes through unchanged.
func Middleware(extract ExtractFunc, store Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if extract == nil || validate.IsNil(store) || validate.IsNil(next) {
			return middlewareUnavailableHandler()
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ref, err := extract(r)
			if errors.Is(err, ErrNoTenant) {
				next.ServeHTTP(w, r)
				return
			}
			if err != nil {
				http.Error(w, "tenancy: invalid tenant reference", http.StatusBadRequest)
				return
			}
			if ref.Value == "" {
				next.ServeHTTP(w, r)
				return
			}

			t, err := resolveTenant(r.Context(), store, ref)
			if err != nil {
				writeResolveError(w, err)
				return
			}

			ctx := context.WithValue(r.Context(), contextKey{}, cloneTenant(t))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func middlewareUnavailableHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "tenancy: middleware dependency unavailable", http.StatusInternalServerError)
	})
}

func resolveTenant(ctx context.Context, store Store, ref TenantRef) (Tenant, error) {
	ref.Value = strings.TrimSpace(ref.Value)
	if ref.Value == "" {
		return Tenant{}, errInvalidTenantRefKind
	}
	var (
		tenant Tenant
		err    error
	)
	switch ref.Kind {
	case TenantRefByID:
		tenant, err = store.FindByID(ctx, ref.Value)
	case TenantRefBySlug:
		tenant, err = store.FindBySlug(ctx, ref.Value)
	default:
		return Tenant{}, errInvalidTenantRefKind
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("tenancy: resolve reference: %w", err)
	}
	return tenant, nil
}

func writeResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrTenantNotFound):
		http.Error(w, "tenancy: tenant not found", http.StatusUnauthorized)
	case errors.Is(err, errInvalidTenantRefKind):
		http.Error(w, "tenancy: invalid tenant reference", http.StatusBadRequest)
	default:
		http.Error(w, "tenancy: tenant lookup failed", http.StatusInternalServerError)
	}
}

var errInvalidTenantRefKind = errors.New("tenancy: invalid tenant reference kind")

func cloneTenant(t Tenant) Tenant {
	t.Metadata = maps.Clone(t.Metadata)
	return t
}

// FromContext returns the Tenant stored by [Middleware]. ok is false when
// the request is not tenant-scoped.
func FromContext(ctx context.Context) (Tenant, bool) {
	t, ok := ctx.Value(contextKey{}).(Tenant)
	return cloneTenant(t), ok
}

// ErrMissingTenant is returned by [RequireFromContext] when the context
// carries no tenant — usually a handler mounted outside [Middleware].
var ErrMissingTenant = errors.New("tenancy: no tenant in context — did you forget Middleware?")

// RequireFromContext returns the Tenant stored by [Middleware], or
// [ErrMissingTenant] when the request is not tenant-scoped.
func RequireFromContext(ctx context.Context) (Tenant, error) {
	t, ok := FromContext(ctx)
	if !ok {
		return Tenant{}, ErrMissingTenant
	}
	return t, nil
}

// --- Built-in extractors ---

// HeaderExtractor returns an ExtractFunc that reads the tenant ID from
// a request header (e.g. "X-Tenant-ID"). Returns [ErrNoTenant] when header
// is absent.
func HeaderExtractor(header string) ExtractFunc {
	return func(r *http.Request) (TenantRef, error) {
		v := strings.TrimSpace(r.Header.Get(header))
		if v == "" {
			return TenantRef{}, ErrNoTenant
		}
		return TenantRef{Kind: TenantRefByID, Value: v}, nil
	}
}

// SubdomainExtractor returns an ExtractFunc that extracts the first label of
// the request host and treats it as the tenant slug. baseDomain is stripped
// (e.g. "example.com") — if the host equals baseDomain (no subdomain) or is
// "www", [ErrNoTenant] is returned.
func SubdomainExtractor(baseDomain string) ExtractFunc {
	baseDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(baseDomain), "."))
	return func(r *http.Request) (TenantRef, error) {
		if baseDomain == "" {
			return TenantRef{}, ErrNoTenant
		}
		host := r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		base := "." + baseDomain
		slug, ok := strings.CutSuffix(host, base)
		if !ok || strings.Contains(slug, ".") {
			return TenantRef{}, ErrNoTenant
		}
		if slug == "" || slug == "www" {
			return TenantRef{}, ErrNoTenant
		}
		return TenantRef{Kind: TenantRefBySlug, Value: slug}, nil
	}
}
