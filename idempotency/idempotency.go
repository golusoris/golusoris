// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package idempotency provides HTTP middleware and a gRPC unary interceptor
// that enforce idempotency keys (draft-ietf-httpapi-idempotency-key-header).
// On the first request for a key the middleware reserves the scoped key,
// captures a bounded response, and stores it. Completed retries replay the
// response; concurrent retries fail with HTTP 409 (gRPC Aborted).
//
// Usage:
//
//	mux.Handle("/payments", idempotency.Middleware(store, idempotency.Options{})(payHandler))
//
// The caller supplies a [Store]: [MemoryStore] for one process or tests,
// [PostgresStore] or [RedisStore] shared across replicas, [SQLiteStore] for
// a standalone node.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/tenancy"
)

const (
	defaultBodyLimit  = int64(1 << 20)
	storeWriteTimeout = 5 * time.Second
)

// ScopeFunc returns an additional caller scope for an idempotency key.
// Use it for principals outside the golusoris tenancy context.
type ScopeFunc func(r *http.Request) (string, error)

// NewScopeFunc stores scope behind a comparable pointer for [Options].
// A nil callback returns nil.
func NewScopeFunc(scope ScopeFunc) *ScopeFunc {
	if scope == nil {
		return nil
	}
	return &scope
}

// Options tunes middleware behaviour.
type Options struct {
	// Header is the request header carrying the idempotency key.
	// Default: "Idempotency-Key".
	Header string
	// TTL is how long reservations and completed responses are retained.
	// Default: 24h.
	TTL time.Duration
	// Required, when true, rejects requests without the header (HTTP 400).
	// Default: false (header is optional; requests without it pass through).
	Required bool
	// MaxRequestBody bounds the payload buffered for fingerprinting.
	// Default: 1 MiB.
	MaxRequestBody int64
	// MaxResponseBody bounds the response retained for replay. Larger responses
	// still reach the first caller but are not cached. Default: 1 MiB.
	MaxResponseBody int64
	// Scope adds a principal or application scope to the built-in method,
	// host, target, and tenancy-ID scope.
	Scope *ScopeFunc
	// Logger receives store and replay-write failures. nil uses slog.Default().
	Logger *slog.Logger
}

func (o *Options) defaults() {
	if o.Header == "" {
		o.Header = "Idempotency-Key"
	}
	if o.TTL <= 0 {
		o.TTL = 24 * time.Hour
	}
	if o.MaxRequestBody <= 0 {
		o.MaxRequestBody = defaultBodyLimit
	}
	if o.MaxResponseBody <= 0 {
		o.MaxResponseBody = defaultBodyLimit
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Scope != nil && *o.Scope != nil {
		scope := *o.Scope
		o.Scope = &scope
	}
}

// Middleware returns an HTTP middleware that enforces idempotency for
// non-safe methods (POST, PUT, PATCH, DELETE).
func Middleware(store Store, opts Options) func(http.Handler) http.Handler {
	opts.defaults()
	return func(next http.Handler) http.Handler {
		if validate.IsNil(next) {
			return unavailableHandler()
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			key := r.Header.Get(opts.Header)
			if key == "" {
				handleMissingKey(w, r, next, opts)
				return
			}
			serveWithKey(w, r, next, store, opts, key)
		})
	}
}

func unavailableHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "idempotency: middleware dependency unavailable", http.StatusInternalServerError)
	})
}

// isSafeMethod reports whether method is exempt from idempotency enforcement.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func handleMissingKey(w http.ResponseWriter, r *http.Request, next http.Handler, opts Options) {
	if opts.Required {
		http.Error(w, "missing "+opts.Header, http.StatusBadRequest)
		return
	}
	next.ServeHTTP(w, r)
}

func serveWithKey(w http.ResponseWriter, r *http.Request, next http.Handler, store Store, opts Options, rawKey string) {
	if validate.IsNil(store) {
		http.Error(w, "idempotency: store error", http.StatusInternalServerError)
		return
	}
	fingerprint, err := fingerprintRequest(r, opts.MaxRequestBody)
	if err != nil {
		writeFingerprintError(w, err)
		return
	}
	key, err := scopedKey(r, rawKey, opts.Scope)
	if err != nil {
		http.Error(w, "idempotency: scope error", http.StatusInternalServerError)
		return
	}
	claim, err := store.Claim(r.Context(), key, fingerprint, opts.TTL)
	if err != nil {
		http.Error(w, "idempotency: store error", http.StatusInternalServerError)
		return
	}
	handleClaim(w, r, next, store, opts, key, fingerprint, claim)
}

func handleClaim(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	store Store,
	opts Options,
	key string,
	fingerprint string,
	claim ClaimResult,
) {
	switch claim.State {
	case ClaimAcquired:
		if claim.Token == "" {
			http.Error(w, "idempotency: invalid store claim", http.StatusInternalServerError)
			return
		}
		serveClaimed(w, r, next, store, opts, key, fingerprint, claim.Token)
	case ClaimInFlight:
		http.Error(w, ErrConflict.Error(), http.StatusConflict)
	case ClaimFingerprintMismatch:
		http.Error(w, ErrFingerprintMismatch.Error(), http.StatusUnprocessableEntity)
	case ClaimCompleted:
		if claim.Fingerprint != fingerprint {
			http.Error(w, ErrFingerprintMismatch.Error(), http.StatusUnprocessableEntity)
			return
		}
		if claim.Response.StatusCode < http.StatusOK || claim.Response.StatusCode > 599 {
			http.Error(w, "idempotency: invalid stored response", http.StatusInternalServerError)
			return
		}
		replay(r.Context(), w, claim.Response, opts.Logger)
	default:
		http.Error(w, "idempotency: invalid store claim", http.StatusInternalServerError)
	}
}

func serveClaimed(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	store Store,
	opts Options,
	key string,
	fingerprint string,
	token string,
) {
	held := true
	defer func(ctx context.Context) {
		if held {
			releaseClaim(ctx, store, key, token, opts.Logger)
		}
	}(r.Context())
	resp, overflow := captureResponse(w, next, r, opts.MaxResponseBody)
	if overflow || resp.StatusCode >= http.StatusInternalServerError {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), storeWriteTimeout)
	defer cancel()
	if err := store.Commit(ctx, key, token, fingerprint, resp, opts.TTL); err != nil {
		opts.Logger.WarnContext(ctx, "idempotency: commit response", slog.Any("err", err))
		return
	}
	held = false
}

func releaseClaim(parent context.Context, store Store, key, token string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), storeWriteTimeout)
	defer cancel()
	if err := store.Release(ctx, key, token); err != nil && !errors.Is(err, ErrReservationLost) {
		logger.WarnContext(ctx, "idempotency: release claim", slog.Any("err", err))
	}
}

func fingerprintRequest(r *http.Request, limit int64) (string, error) {
	if limit < 0 {
		return "", errors.New("idempotency: invalid request body limit")
	}
	if r.Body == nil {
		return fingerprintPayload(r.Header.Get("Content-Type"), nil)
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	closeErr := r.Body.Close()
	if err != nil {
		return "", fmt.Errorf("idempotency: read request body: %w", err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("idempotency: close request body: %w", closeErr)
	}
	if int64(len(body)) > limit {
		return "", ErrRequestBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return fingerprintPayload(r.Header.Get("Content-Type"), body)
}

func fingerprintPayload(contentType string, body []byte) (string, error) {
	digest := sha256.New()
	if err := writeHashPart(digest, []byte(contentType)); err != nil {
		return "", fmt.Errorf("idempotency: hash content type: %w", err)
	}
	if err := writeHashPart(digest, body); err != nil {
		return "", fmt.Errorf("idempotency: hash request body: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func scopedKey(r *http.Request, rawKey string, scope *ScopeFunc) (string, error) {
	extraScope := ""
	if scope != nil {
		if *scope == nil {
			return "", errors.New("idempotency: nil scope callback")
		}
		var err error
		extraScope, err = (*scope)(r)
		if err != nil {
			return "", fmt.Errorf("idempotency: resolve scope: %w", err)
		}
	}
	tenantID := ""
	if tenant, ok := tenancy.FromContext(r.Context()); ok {
		tenantID = tenant.ID
	}
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	target := path
	if query := r.URL.Query().Encode(); query != "" {
		target += "?" + query
	}
	return digestScope("v1:", strings.ToUpper(r.Method), strings.ToLower(r.Host), target, tenantID, extraScope, rawKey)
}

// digestScope hashes length-prefixed parts so no two part lists collide.
func digestScope(prefix string, parts ...string) (string, error) {
	digest := sha256.New()
	for _, part := range parts {
		if err := writeHashPart(digest, []byte(part)); err != nil {
			return "", fmt.Errorf("idempotency: hash scoped key: %w", err)
		}
	}
	return prefix + hex.EncodeToString(digest.Sum(nil)), nil
}

// Request-derived parts only ever feed a digest, never a response or log writer.
func writeHashPart(dst hash.Hash, value []byte) error {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	if err := writeHashBytes(dst, size[:]); err != nil {
		return err
	}
	return writeHashBytes(dst, value)
}

func writeHashBytes(dst hash.Hash, value []byte) error {
	written, err := dst.Write(value)
	if err != nil {
		return fmt.Errorf("write digest input: %w", err)
	}
	if written != len(value) {
		return io.ErrShortWrite
	}
	return nil
}

func writeFingerprintError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrRequestBodyTooLarge) {
		http.Error(w, ErrRequestBodyTooLarge.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "idempotency: request body error", http.StatusBadRequest)
}

func captureResponse(
	w http.ResponseWriter,
	next http.Handler,
	r *http.Request,
	limit int64,
) (CachedResponse, bool) {
	recorder := &responseRecorder{target: w, limit: limit}
	next.ServeHTTP(recorder, r)
	if !recorder.wroteHeader {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.response(), recorder.overflow
}

func replay(ctx context.Context, w http.ResponseWriter, response CachedResponse, logger *slog.Logger) {
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := response.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if _, err := w.Write(response.Body); err != nil {
		logger.DebugContext(ctx, "idempotency: write response", slog.Any("err", err))
	}
}

type responseRecorder struct {
	target      http.ResponseWriter
	header      http.Header
	code        int
	body        []byte
	limit       int64
	wroteHeader bool
	overflow    bool
}

func (r *responseRecorder) Header() http.Header { return r.target.Header() }

func (r *responseRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.code = code
	r.header = r.target.Header().Clone()
	r.target.WriteHeader(code)
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	written, err := r.target.Write(body)
	r.capture(body[:written])
	if err != nil {
		return written, fmt.Errorf("idempotency: write response: %w", err)
	}
	return written, nil
}

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.target }

func (r *responseRecorder) capture(body []byte) {
	remaining := r.limit - int64(len(r.body))
	if remaining <= 0 {
		r.overflow = r.overflow || len(body) > 0
		return
	}
	if int64(len(body)) > remaining {
		body = body[:remaining]
		r.overflow = true
	}
	r.body = append(r.body, body...)
}

func (r *responseRecorder) response() CachedResponse {
	return CachedResponse{
		StatusCode: r.code,
		Header:     r.header.Clone(),
		Body:       bytes.Clone(r.body),
	}
}
