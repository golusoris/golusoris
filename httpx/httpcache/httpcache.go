// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package httpcache provides outbound HTTP response caching over cache/twotier and bartventer/httpcache.
//
// Compose Cache.Wrap(limiter.Wrap(base)) as httpx/client Options.Transport.
// Use distinct prefixes for different principals sharing storage.
// Each instance serves one principal; secret query parameters are removed from
// storage keys, and X-Httpcache-Status reports HIT, MISS, STALE or REVALIDATED.
// Credentialed responses require an RFC 9111 section 3.5 sharing directive or a shared host.
// Dragonfly and Redis can provide L2; store.l2=none uses only memory.
// With l2=none, retention controls L1 TTL; zero preserves the memory cache TTL.
//
// Config keys under http.outbound.cache:
//
//	prefix, max_body_bytes, max_in_flight, revalidate_timeout, store_timeout,
//	stale_if_error, stale_while_revalidate, secret_params, secret_headers,
//	credential_fields, auth_path_segments, hosts,
//	store.l1_ttl, store.retention, store.l2
//
// Host entries have host, ttl, stale_if_error, stale_while_revalidate,
// auth_path_segments, shared and disabled. Zero host durations inherit defaults.
// Secret and path lists extend built-in protections. Construct options with
// DefaultOptions, then override fields; retention=0 retains L2 entries indefinitely.
package httpcache

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bartventer/httpcache"

	"github.com/golusoris/golusoris/cache/singleflight"
	"github.com/golusoris/golusoris/cache/twotier"
	"github.com/golusoris/golusoris/core/validate"
)

// Options configures http.outbound.cache; use DefaultOptions as a starting point.
type Options struct {
	Prefix               string        `koanf:"prefix"`
	MaxBodyBytes         int64         `koanf:"max_body_bytes"`
	MaxInFlight          int           `koanf:"max_in_flight"`
	RevalidateTimeout    time.Duration `koanf:"revalidate_timeout"`
	StoreTimeout         time.Duration `koanf:"store_timeout"`
	StaleIfError         time.Duration `koanf:"stale_if_error"`
	StaleWhileRevalidate time.Duration `koanf:"stale_while_revalidate"`
	SecretParams         []string      `koanf:"secret_params"`
	SecretHeaders        []string      `koanf:"secret_headers"`
	CredentialFields     []string      `koanf:"credential_fields"`
	AuthPathSegments     []string      `koanf:"auth_path_segments"`
	Hosts                []HostOptions `koanf:"hosts"`
	Store                StoreOptions  `koanf:"store"`
}

// HostOptions overrides one exact, case-insensitive hostname without a port.
// Zero durations inherit defaults; AuthPathSegments extends global segments.
type HostOptions struct {
	Host                 string        `koanf:"host"`
	TTL                  time.Duration `koanf:"ttl"`
	StaleIfError         time.Duration `koanf:"stale_if_error"`
	StaleWhileRevalidate time.Duration `koanf:"stale_while_revalidate"`
	AuthPathSegments     []string      `koanf:"auth_path_segments"`
	Shared               bool          `koanf:"shared"` // Responses do not depend on caller credentials.
	Disabled             bool          `koanf:"disabled"`
}

// StoreOptions configures the private two-tier view; retention is entry retention, not freshness.
type StoreOptions struct {
	L1TTL     time.Duration `koanf:"l1_ttl"`
	Retention time.Duration `koanf:"retention"`
	L2        string        `koanf:"l2"`
}

// DefaultOptions returns bounded defaults and a Redis-compatible L2.
func DefaultOptions() Options {
	return Options{
		Prefix: "httpcache", MaxBodyBytes: 1 << 20, MaxInFlight: 64,
		RevalidateTimeout: 5 * time.Second, StoreTimeout: 2 * time.Second, StaleIfError: time.Hour,
		Store:            StoreOptions{L1TTL: 30 * time.Second, Retention: 24 * time.Hour, L2: twotier.L2Redis},
		SecretParams:     append([]string(nil), defaultSecretParams...),
		SecretHeaders:    append([]string(nil), defaultSecretHeaders...),
		CredentialFields: append([]string(nil), defaultCredentialFields...),
		AuthPathSegments: append([]string(nil), defaultAuthPaths...),
	}
}

// Cache owns a registered storage connection and bounded upstream flights.
type Cache struct {
	opts          Options
	id            string
	logger        *slog.Logger
	hosts         map[string]HostOptions
	secrets       map[string]struct{}
	secretHeaders map[string]struct{}
	authPaths     map[string]struct{}
	credentials   *regexp.Regexp
	fields        map[string]struct{}
	slots         chan struct{}
	bgSlots       chan struct{}
	flights       *singleflight.Group[flightKey, *flightResult]
	mu            sync.Mutex
	closed        bool
}

// New constructs a cache over store; invalid bounds or missing dependencies fail.
func New(opts Options, store *twotier.TwoTier, logger *slog.Logger) (*Cache, error) {
	if store == nil || logger == nil {
		return nil, errors.New("httpcache: store and logger required")
	}
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if opts.StoreTimeout == 0 {
		opts.StoreTimeout = DefaultOptions().StoreTimeout
	}
	if opts.RevalidateTimeout == 0 {
		opts.RevalidateTimeout = DefaultOptions().RevalidateTimeout
	}
	if opts.Prefix == "" {
		opts.Prefix = "httpcache"
	}
	c := &Cache{
		opts: opts, id: rand.Text(), logger: logger,
		hosts:         make(map[string]HostOptions, len(opts.Hosts)),
		secrets:       canonicalSet(defaultSecretParams, opts.SecretParams),
		secretHeaders: extendedSet(defaultSecretHeaders, opts.SecretHeaders),
		authPaths:     extendedSet(defaultAuthPaths, opts.AuthPathSegments),
		fields:        canonicalSet(defaultCredentialFields, opts.CredentialFields),
		credentials:   credentialPattern(opts.CredentialFields), slots: make(chan struct{}, opts.MaxInFlight),
		bgSlots: make(chan struct{}, opts.MaxInFlight),
		flights: singleflight.New[flightKey, *flightResult](),
	}
	for _, host := range opts.Hosts {
		c.hosts[strings.ToLower(host.Host)] = host
	}
	registerDriver()
	instances.Store(c.id, &connection{view: twotier.NewTyped[[]byte](store, opts.Prefix), timeout: opts.StoreTimeout})
	return c, nil
}

func validateOptions(opts Options) error {
	if opts.MaxBodyBytes <= 0 || opts.MaxBodyBytes == 1<<63-1 || opts.MaxInFlight <= 0 {
		return errors.New("httpcache: positive bounded max_body_bytes and max_in_flight required")
	}
	durations := []time.Duration{
		opts.RevalidateTimeout, opts.StoreTimeout, opts.StaleIfError,
		opts.StaleWhileRevalidate, opts.Store.L1TTL, opts.Store.Retention,
	}
	for _, duration := range durations {
		if duration < 0 {
			return errors.New("httpcache: negative duration")
		}
	}
	if opts.Store.L2 != "" && opts.Store.L2 != twotier.L2None && opts.Store.L2 != twotier.L2Redis {
		return errors.New("httpcache: unknown store.l2")
	}
	return validateHosts(opts)
}

func validateHosts(opts Options) error {
	seen := make(map[string]struct{}, len(opts.Hosts))
	maxRetention, err := cacheWindow(resolveHost(opts, HostOptions{}))
	if err != nil {
		return err
	}
	for _, host := range opts.Hosts {
		name := strings.ToLower(host.Host)
		if err := validate.New().Var(name, "hostname_rfc1123|ip"); err != nil {
			return fmt.Errorf("httpcache: invalid host: %w", err)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("httpcache: duplicate host %q", name)
		}
		seen[name] = struct{}{}
		total, windowErr := cacheWindow(resolveHost(opts, host))
		if windowErr != nil {
			return windowErr
		}
		maxRetention = max(maxRetention, total)
	}
	if opts.Store.Retention != 0 && opts.Store.Retention < maxRetention {
		return errors.New("httpcache: retention shorter than freshness and stale windows")
	}
	return nil
}

func cacheWindow(host HostOptions) (time.Duration, error) {
	var total time.Duration
	for _, duration := range [...]time.Duration{host.TTL, host.StaleIfError, host.StaleWhileRevalidate} {
		if duration < 0 || duration > (1<<63-1)-total {
			return 0, errors.New("httpcache: negative or overflowing cache window")
		}
		total += duration
	}
	return total, nil
}

func resolveHost(opts Options, host HostOptions) HostOptions {
	if host.StaleIfError == 0 {
		host.StaleIfError = opts.StaleIfError
	}
	if host.StaleWhileRevalidate == 0 {
		host.StaleWhileRevalidate = opts.StaleWhileRevalidate
	}
	return host
}

func (c *Cache) host(req *http.Request) HostOptions {
	return resolveHost(c.opts, c.hosts[strings.ToLower(req.URL.Hostname())])
}

// Close deregisters this instance and prevents construction of new wrappers.
func (c *Cache) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	instances.Delete(c.id)
	return nil
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// Wrap constructs outer policy, RFC transport and inner upstream policy layers.
// A nil upstream or closed cache returns an error transport.
func (c *Cache) Wrap(next http.RoundTripper) http.RoundTripper {
	c.mu.Lock()
	defer c.mu.Unlock()
	if validate.IsNil(next) || c.closed {
		return transportFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("httpcache: open cache and next transport required")
		})
	}
	inner := transportFunc(func(req *http.Request) (*http.Response, error) { return c.upstream(req, next) })
	cached := httpcache.NewTransport(driverScheme+"://"+c.id, httpcache.WithUpstream(inner),
		httpcache.WithSWRTimeout(c.opts.RevalidateTimeout), httpcache.WithLogger(c.logger))
	return transportFunc(func(req *http.Request) (*http.Response, error) { return c.roundTrip(req, cached, next) })
}

type (
	requestStateKey struct{}
	requestState    struct {
		rawQuery     string
		failure      atomic.Pointer[upstreamFailure]
		conditional  bool
		credentialed bool
		done         <-chan struct{} // engine background revalidation derives a new Done channel
		user         *url.Userinfo
	}
)

func stateOf(req *http.Request) (*requestState, bool) {
	state, ok := req.Context().Value(requestStateKey{}).(*requestState)
	return state, ok
}

var validatorHeaders = [...]string{"If-None-Match", "If-Modified-Since"}

func hasValidators(header http.Header) bool {
	for _, name := range validatorHeaders {
		if header.Get(name) != "" {
			return true
		}
	}
	return false
}

type upstreamFailure struct {
	err      error
	response *http.Response
}

const syntheticHeader = "X-Golusoris-Httpcache-Synthetic"

var completedFailure = &upstreamFailure{}

func (c *Cache) roundTrip(req *http.Request, cached, next http.RoundTripper) (*http.Response, error) {
	if c.bypass(req) {
		resp, err := next.RoundTrip(req)
		if err != nil {
			return nil, fmt.Errorf("httpcache: bypass upstream: %w", err)
		}
		return resp, nil
	}
	state := &requestState{conditional: hasValidators(req.Header)}
	ctx, unlink, release := detach(req.Context())
	stripped := req.Clone(context.WithValue(ctx, requestStateKey{}, state))
	state.done = stripped.Context().Done()
	c.stripSecrets(stripped, state)
	resp, err := cached.RoundTrip(stripped)
	failure := state.failure.Swap(completedFailure)
	if err != nil {
		release()
		return nil, errors.Join(fmt.Errorf("httpcache: round trip: %w", err), closeFailure(failure, nil))
	}
	resp, err = resolveResponse(resp, failure)
	if err != nil {
		release()
		return nil, err
	}
	if resp.Header.Get(httpcache.CacheStatusHeader) == "STALE" {
		// Cached body; a background revalidation may still run under its own timeout.
		unlink()
		return resp, nil
	}
	resp.Body = &linkedBody{ReadCloser: resp.Body, release: release}
	return resp, nil
}

// detach returns a context the caller cannot cancel, because library revalidation goroutines inherit it.
// The caller's cancellation is forwarded until unlink; release also cancels the context.
func detach(parent context.Context) (context.Context, func() bool, func()) {
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	unlink := context.AfterFunc(parent, func() { cancel(context.Cause(parent)) })
	return ctx, unlink, func() { unlink(); cancel(nil) }
}

// linkedBody keeps upstream work cancellable by the caller until the body is closed.
type linkedBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *linkedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	if err != nil {
		return fmt.Errorf("httpcache: close body: %w", err)
	}
	return nil
}

func closeFailure(failure *upstreamFailure, keep *http.Response) error {
	if failure == nil || failure.response == nil || failure.response == keep {
		return nil
	}
	if err := failure.response.Body.Close(); err != nil {
		return fmt.Errorf("httpcache: close unused failure: %w", err)
	}
	return nil
}

func (c *Cache) stripSecrets(stripped *http.Request, state *requestState) {
	state.user = stripped.URL.User
	stripped.URL.User = nil
	state.credentialed = state.user != nil
	for name := range stripped.Header {
		if _, secret := c.secretHeaders[strings.ToLower(name)]; secret {
			state.credentialed = true
		}
	}
	state.rawQuery = stripped.URL.RawQuery
	if !c.secretQuery(state.rawQuery) {
		return
	}
	state.credentialed = true
	segments := strings.Split(state.rawQuery, "&")
	kept := segments[:0]
	for _, segment := range segments {
		if !c.secretSegment(segment) {
			kept = append(kept, segment)
		}
	}
	stripped.URL.RawQuery = strings.Join(kept, "&")
}

func resolveResponse(resp *http.Response, failure *upstreamFailure) (*http.Response, error) {
	if resp.Header.Get(syntheticHeader) != "" {
		if closeErr := discardSynthetic(resp); closeErr != nil {
			return nil, closeErr
		}
		if failure == nil {
			return nil, errors.New("httpcache: synthetic response without failure")
		}
		if failure.err != nil {
			return nil, fmt.Errorf("httpcache: upstream: %w", failure.err)
		}
		return failure.response, nil
	}
	if err := closeFailure(failure, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func discardSynthetic(resp *http.Response) error {
	_, drainErr := io.Copy(io.Discard, resp.Body)
	if err := errors.Join(drainErr, resp.Body.Close()); err != nil {
		return fmt.Errorf("httpcache: discard synthetic: %w", err)
	}
	return nil
}

func recordFailure(state *requestState, failure *upstreamFailure) error {
	// A background revalidation may finish after the outer caller has returned.
	if !state.failure.CompareAndSwap(nil, failure) && failure.response != nil {
		if err := failure.response.Body.Close(); err != nil {
			return fmt.Errorf("httpcache: close background failure: %w", err)
		}
	}
	return nil
}

func (c *Cache) bypass(req *http.Request) bool {
	host := c.host(req)
	if host.Disabled {
		return true
	}
	for segment := range strings.SplitSeq(req.URL.Path, "/") {
		if _, found := c.authPaths[strings.ToLower(segment)]; found {
			return true
		}
		for _, extra := range host.AuthPathSegments {
			if strings.EqualFold(extra, segment) {
				return true
			}
		}
	}
	return false
}

type flightKey struct {
	method, url string
	headers     [6]string
	conditional bool
}

func upstreamKey(req *http.Request) flightKey {
	key := flightKey{method: req.Method, url: req.URL.String()}
	if state, ok := stateOf(req); ok {
		key.conditional = state.conditional
	}
	for i, name := range [...]string{"Accept", "Accept-Language", "Accept-Encoding", "If-None-Match", "If-Modified-Since", "Range"} {
		key.headers[i] = strings.Join(req.Header.Values(name), "\x00")
	}
	return key
}

type flightResult struct {
	response  *http.Response
	header    http.Header
	body      []byte
	streaming bool
}

func (c *Cache) upstream(req *http.Request, next http.RoundTripper) (*http.Response, error) {
	if state, ok := stateOf(req); ok && req.Context().Done() != state.done {
		select {
		case c.bgSlots <- struct{}{}:
			defer func() { <-c.bgSlots }()
		default:
			return nil, errors.New("httpcache: revalidation budget exhausted")
		}
	}
	result, err := c.flight(req, next)
	if err != nil {
		return c.failure(req, err)
	}
	resp := copyResponse(result, req)
	return c.upstreamResponse(req, resp, result.streaming)
}

func (c *Cache) flight(req *http.Request, next http.RoundTripper) (*flightResult, error) {
	var leader atomic.Bool
	call := func(context.Context) (*flightResult, error) { leader.Store(true); return c.fetchResolved(req, next) }
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return call(req.Context())
	}
	result, _, err := c.flights.DoCancelable(req.Context(), upstreamKey(req), call)
	if err != nil {
		if !liveFollower(req, err, leader.Load()) {
			return nil, fmt.Errorf("httpcache: flight: %w", err)
		}
		return c.fetchResolved(req, next)
	}
	if !leader.Load() && (result.streaming || varyMismatch(result.response.Header, result.header, req.Header)) {
		return c.fetchResolved(req, next)
	}
	return result, nil
}

func liveFollower(req *http.Request, err error, leader bool) bool {
	return !leader && req.Context().Err() == nil && canceledFlight(err)
}

func canceledFlight(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (c *Cache) fetchResolved(req *http.Request, next http.RoundTripper) (*flightResult, error) {
	result, err := c.fetch(req, next)
	if err == nil && unmergeable304(req, result.response) {
		return c.fetchFull(req, next, result)
	}
	return result, err
}

// unmergeable304 reports a no-store 304 answering validators the caller never sent.
func unmergeable304(req *http.Request, resp *http.Response) bool {
	state, ok := stateOf(req)
	return ok && !state.conditional && resp.StatusCode == http.StatusNotModified && hasDirective(resp.Header, "no-store")
}

// fetchFull replaces an unmergeable 304 with an unconditional upstream response.
func (c *Cache) fetchFull(req *http.Request, next http.RoundTripper, notModified *flightResult) (*flightResult, error) {
	if notModified.streaming {
		if err := notModified.response.Body.Close(); err != nil {
			return nil, fmt.Errorf("httpcache: close not modified: %w", err)
		}
	}
	full := req.Clone(req.Context())
	for _, name := range validatorHeaders {
		full.Header.Del(name)
	}
	return c.fetch(full, next)
}

func (c *Cache) upstreamResponse(req *http.Request, resp *http.Response, streaming bool) (*http.Response, error) {
	state, ok := stateOf(req)
	if !ok {
		return resp, nil
	}
	// The pinned engine stores 304 merges without checking no-store; conditional callers get the 304 itself.
	if resp.StatusCode == http.StatusNotModified && hasDirective(resp.Header, "no-store") {
		if err := recordFailure(state, &upstreamFailure{response: resp}); err != nil {
			return nil, err
		}
		return markedResponse(req, http.StatusBadGateway), nil
	}
	if streaming || resp.StatusCode == http.StatusTooManyRequests {
		if err := recordFailure(state, &upstreamFailure{response: resp}); err != nil {
			return nil, err
		}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return c.synthetic(req, http.StatusServiceUnavailable), nil
	}

	return resp, nil
}

func (c *Cache) failure(req *http.Request, err error) (*http.Response, error) {
	if req.Context().Err() != nil {
		return nil, fmt.Errorf("httpcache: canceled: %w", context.Cause(req.Context()))
	}
	state, ok := stateOf(req)
	if !ok {
		return nil, fmt.Errorf("httpcache: upstream: %w", err)
	}
	state.failure.CompareAndSwap(nil, &upstreamFailure{err: err})
	return c.synthetic(req, http.StatusGatewayTimeout), nil
}

func markedResponse(req *http.Request, status int) *http.Response {
	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header: http.Header{"Cache-Control": {"no-store"}, syntheticHeader: {"1"}}, Body: http.NoBody, Request: req,
	}
}

func (c *Cache) synthetic(req *http.Request, status int) *http.Response {
	resp := markedResponse(req, status)
	// The pinned engine reads the error response's stale window during validation.
	addStale(resp.Header, "stale-if-error", c.host(req).StaleIfError)
	return resp
}

func (c *Cache) fetch(req *http.Request, next http.RoundTripper) (*flightResult, error) {
	select {
	case <-req.Context().Done():
		return nil, fmt.Errorf("httpcache: acquire: %w", context.Cause(req.Context()))
	case c.slots <- struct{}{}:
	}
	release := true
	defer func() {
		if release {
			<-c.slots
		}
	}()
	restored := restoreRequest(req)
	resp, err := next.RoundTrip(restored)
	if err != nil {
		return nil, fmt.Errorf("httpcache: upstream: %w", err)
	}
	resp.Request = req
	c.applyHeaders(resp, c.host(req))
	if hasDirective(resp.Header, "no-store") {
		release = false
		return c.streamingResult(resp, nil), nil
	}
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, c.opts.MaxBodyBytes+1))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("httpcache: read body: %w", err), resp.Body.Close())
	}
	oversized := int64(len(prefix)) > c.opts.MaxBodyBytes
	c.applyBody(resp, prefix, oversized)
	if oversized {
		release = false
		return c.streamingResult(resp, prefix), nil
	}
	if err := resp.Body.Close(); err != nil {
		return nil, fmt.Errorf("httpcache: close upstream: %w", err)
	}
	return &flightResult{response: resp, body: prefix, header: req.Header.Clone()}, nil
}

func restoreRequest(req *http.Request) *http.Request {
	restored := req.Clone(req.Context())
	if state, ok := stateOf(req); ok {
		restored.URL.User = state.user
		restored.URL.RawQuery = state.rawQuery
	}
	return restored
}

func copyResponse(result *flightResult, req *http.Request) *http.Response {
	resp := *result.response
	resp.Header = result.response.Header.Clone()
	resp.Trailer = result.response.Trailer
	resp.Request = req
	if !result.streaming {
		resp.Trailer = result.response.Trailer.Clone()
		resp.Body = io.NopCloser(bytes.NewReader(result.body))
	}
	return &resp
}

type streamBody struct {
	io.Reader
	original io.Closer
	release  func()
	once     sync.Once
	err      error
}

func (b *streamBody) Close() error {
	b.once.Do(func() { b.err = b.original.Close(); b.release() })
	if b.err != nil {
		return fmt.Errorf("httpcache: close streaming body: %w", b.err)
	}
	return nil
}

func (c *Cache) secretQuery(raw string) bool {
	for segment := range strings.SplitSeq(raw, "&") {
		if c.secretSegment(segment) {
			return true
		}
	}
	return false
}

func (c *Cache) secretSegment(segment string) bool {
	for part := range strings.SplitSeq(segment, ";") {
		key, _, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err == nil {
			key = decoded
		}
		if _, secret := c.secrets[canonicalName(key)]; secret {
			return true
		}
	}
	return false
}

func (c *Cache) streamingResult(resp *http.Response, prefix []byte) *flightResult {
	resp.Body = &streamBody{Reader: io.MultiReader(bytes.NewReader(prefix), resp.Body), original: resp.Body, release: func() { <-c.slots }}
	return &flightResult{response: resp, streaming: true, header: resp.Request.Header.Clone()}
}

func (b *streamBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		if closeErr := b.Close(); closeErr != nil {
			return n, closeErr
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("httpcache: read streaming body: %w", err)
	}
	return n, err //nolint:wrapcheck // WHY: preserve the original EOF sentinel required by io.Reader.
}

func (b *linkedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) {
		b.once.Do(b.release)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("httpcache: read linked body: %w", err)
	}
	return n, err //nolint:wrapcheck // WHY: preserve the original EOF sentinel required by io.Reader.
}

func varyMismatch(response, leader, follower http.Header) bool {
	for name := range strings.SplitSeq(strings.Join(response.Values("Vary"), ","), ",") {
		name = strings.TrimSpace(name)
		if name == "*" {
			return true
		}
		if name != "" && strings.Join(leader.Values(name), "\x00") != strings.Join(follower.Values(name), "\x00") {
			return true
		}
	}
	return false
}
