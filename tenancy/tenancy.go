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
	"net/http"
	"strings"
)

// Tenant represents a single tenant in a multi-tenant application.
type Tenant struct {
	ID   string
	Slug string
	Plan string
	// Metadata holds app-specific fields (custom domains, feature flags, …).
	Metadata map[string]any
}

// Store resolves a tenant by ID or slug.
type Store interface {
	FindByID(ctx context.Context, id string) (Tenant, error)
	FindBySlug(ctx context.Context, slug string) (Tenant, error)
}

// ExtractFunc extracts the tenant identifier from an incoming request.
// Return ("", nil) to signal "no tenant" (e.g. landing pages).
// Return a non-nil error to reject the request (HTTP 400/404).
type ExtractFunc func(r *http.Request) (id string, err error)

// ErrNoTenant is returned by [ExtractFunc] to signal the request is not
// tenant-scoped (e.g. landing page). The middleware passes through without
// setting context.
var ErrNoTenant = errors.New("tenancy: no tenant")

type contextKey struct{}

// Middleware resolves the tenant for each request and stores it in the context.
// On resolution failure (store error or unknown tenant), it responds 401.
// If extract returns [ErrNoTenant] the middleware passes through unchanged.
func Middleware(extract ExtractFunc, store Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := extract(r)
			if errors.Is(err, ErrNoTenant) {
				next.ServeHTTP(w, r)
				return
			}
			if err != nil {
				http.Error(w, fmt.Sprintf("tenancy: extract: %v", err), http.StatusBadRequest)
				return
			}

			t, err := store.FindByID(r.Context(), id)
			if err != nil {
				http.Error(w, fmt.Sprintf("tenancy: resolve: %v", err), http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), contextKey{}, t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext returns the Tenant stored by [Middleware]. ok is false when
// the request is not tenant-scoped.
func FromContext(ctx context.Context) (Tenant, bool) {
	t, ok := ctx.Value(contextKey{}).(Tenant)
	return t, ok
}

// MustFromContext returns the Tenant or panics. Use only in code paths
// guaranteed to run behind [Middleware].
func MustFromContext(ctx context.Context) Tenant {
	t, ok := FromContext(ctx)
	if !ok {
		panic("tenancy: no tenant in context — did you forget Middleware?")
	}
	return t
}

// --- Built-in extractors ---

// HeaderExtractor returns an ExtractFunc that reads the tenant ID from
// a request header (e.g. "X-Tenant-ID"). Returns [ErrNoTenant] when header
// is absent.
func HeaderExtractor(header string) ExtractFunc {
	return func(r *http.Request) (string, error) {
		v := r.Header.Get(header)
		if v == "" {
			return "", ErrNoTenant
		}
		return v, nil
	}
}

// SubdomainExtractor returns an ExtractFunc that extracts the first label of
// the request host and treats it as the tenant slug. baseDomain is stripped
// (e.g. "example.com") — if the host equals baseDomain (no subdomain) or is
// "www", [ErrNoTenant] is returned.
func SubdomainExtractor(baseDomain string) ExtractFunc {
	return func(r *http.Request) (string, error) {
		host := r.Host
		// Strip port if present.
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		// Strip base domain suffix.
		base := "." + baseDomain
		if !strings.HasSuffix(host, base) {
			return "", ErrNoTenant
		}
		slug := strings.TrimSuffix(host, base)
		if slug == "" || slug == "www" {
			return "", ErrNoTenant
		}
		return slug, nil
	}
}
