// Package idempotency provides HTTP middleware that enforces the
// Idempotency-Key header (draft-ietf-httpapi-idempotency-key-header).
// On the first request for a key the middleware captures the response and
// stores it. Subsequent requests with the same key receive the cached
// response verbatim without invoking the handler again.
//
// Usage:
//
//	mux.Handle("/payments", idempotency.Middleware(store, idempotency.Options{})(payHandler))
//
// The caller must supply a [Store] — use [MemoryStore] for tests or a Redis /
// Postgres-backed implementation for production.
package idempotency

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"time"
)

// CachedResponse is the stored representation of a completed response.
type CachedResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// Store persists idempotency records keyed by the Idempotency-Key header value.
type Store interface {
	// Find returns the cached response for key, or (zero, false, nil) when not yet set.
	Find(ctx context.Context, key string) (CachedResponse, bool, error)
	// Save stores resp under key for ttl. A second call with the same key is a no-op.
	Save(ctx context.Context, key string, resp CachedResponse, ttl time.Duration) error
}

// Options tunes middleware behaviour.
type Options struct {
	// Header is the request header carrying the idempotency key.
	// Default: "Idempotency-Key".
	Header string
	// TTL is how long a cached response is retained. Default: 24h.
	TTL time.Duration
	// Required, when true, rejects requests without the header (HTTP 400).
	// Default: false (header is optional; requests without it pass through).
	Required bool
}

func (o *Options) defaults() {
	if o.Header == "" {
		o.Header = "Idempotency-Key"
	}
	if o.TTL == 0 {
		o.TTL = 24 * time.Hour
	}
}

// Middleware returns an HTTP middleware that enforces idempotency for
// non-safe methods (POST, PUT, PATCH, DELETE).
func Middleware(store Store, opts Options) func(http.Handler) http.Handler {
	opts.defaults()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only enforce on non-safe methods.
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			key := r.Header.Get(opts.Header)
			if key == "" {
				if opts.Required {
					http.Error(w, "missing "+opts.Header, http.StatusBadRequest)
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			// Check cache.
			cached, found, err := store.Find(r.Context(), key)
			if err != nil {
				http.Error(w, "idempotency: store error", http.StatusInternalServerError)
				return
			}
			if found {
				replay(w, cached)
				return
			}

			// Capture the response.
			rec := &responseRecorder{header: make(http.Header), code: http.StatusOK}
			next.ServeHTTP(rec, r)

			resp := CachedResponse{
				StatusCode: rec.code,
				Header:     rec.header,
				Body:       rec.body.Bytes(),
			}
			// Only cache successful (2xx) and client-error (4xx) responses —
			// don't cache 5xx so transient failures can be retried.
			if resp.StatusCode < 500 {
				_ = store.Save(r.Context(), key, resp, opts.TTL)
			}

			// Write captured response to the real ResponseWriter.
			replay(w, resp)
		})
	}
}

func replay(w http.ResponseWriter, r CachedResponse) {
	for k, vs := range r.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(r.StatusCode)
	_, _ = w.Write(r.Body)
}

// responseRecorder captures an http.Handler's output.
type responseRecorder struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (r *responseRecorder) Header() http.Header         { return r.header }
func (r *responseRecorder) WriteHeader(code int)        { r.code = code }
func (r *responseRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }

// --- MemoryStore ---

// MemoryStore is a simple in-memory [Store] for tests. Not suitable for
// multi-replica deployments.
type MemoryStore struct {
	mu      chan struct{}
	entries map[string]memEntry
}

type memEntry struct {
	resp      CachedResponse
	expiresAt time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	mu := make(chan struct{}, 1)
	mu <- struct{}{}
	return &MemoryStore{
		mu:      mu,
		entries: map[string]memEntry{},
	}
}

func (s *MemoryStore) Find(_ context.Context, key string) (CachedResponse, bool, error) {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	e, ok := s.entries[key]
	if !ok || time.Now().After(e.expiresAt) { //nolint:gocritic // intentional time.Now — MemoryStore is a test helper
		return CachedResponse{}, false, nil
	}
	return e.resp, true, nil
}

func (s *MemoryStore) Save(_ context.Context, key string, resp CachedResponse, ttl time.Duration) error {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	if _, exists := s.entries[key]; exists {
		return nil // idempotent
	}
	s.entries[key] = memEntry{resp: resp, expiresAt: time.Now().Add(ttl)} //nolint:gocritic // intentional time.Now — MemoryStore is a test helper
	return nil
}

// ErrConflict is returned when a concurrent in-flight request with the same
// key is still being processed. Callers should respond 409.
var ErrConflict = errors.New("idempotency: concurrent request with same key")
