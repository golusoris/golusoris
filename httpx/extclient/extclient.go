// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package extclient is a pragmatic typed external-API client factory built on
// the framework's outbound client stack ([httpx/client]: retry +
// circuit-breaker + otelhttp + slog).
//
// It is the lightweight alternative to full ogen-codegen from a third-party
// OpenAPI spec: instead of generating a typed client, you configure a [Client]
// per upstream host (base URL, auth, default headers, timeout, optional
// response cache) and call the generic helpers [Get] / [Post] / [Put] /
// [Delete], which marshal/unmarshal JSON into your own caller-defined types.
//
// Usage:
//
//	c, err := extclient.New(extclient.ServiceOptions{
//	    BaseURL: "https://api.example.com",
//	    Bearer:  os.Getenv("EXAMPLE_TOKEN"),
//	})
//	// type User struct { ID string `json:"id"`; Name string `json:"name"` }
//	u, err := extclient.Get[User](ctx, c, "/users/42", nil)
//
// Generic helpers are package-level functions rather than methods because Go
// does not permit type parameters on methods; [Client] itself is non-generic
// so a single instance serves every response type.
package extclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/golusoris/golusoris/cache/memory"
	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/httpx/client"
)

// maxResponseBytes caps how much of a response body we buffer, guarding
// against a hostile or misbehaving upstream streaming an unbounded body.
const maxResponseBytes = 8 << 20 // 8 MiB

var (
	// ErrStatus is returned by the generic helpers when the upstream replies
	// with a non-2xx status. Inspect it with [APIError] via [errors.As].
	ErrStatus = errors.New("extclient: non-2xx status")
	// ErrResponseTooLarge means a successful response exceeded the body cap.
	ErrResponseTooLarge = errors.New("extclient: response body exceeds limit")
)

// APIError carries the status code and (truncated) body of a non-2xx
// response. It wraps [ErrStatus] so callers can branch on either.
type APIError struct {
	// Status is the HTTP status code returned by the upstream.
	Status int
	// Body is the (truncated) response body, useful for surfacing the
	// upstream's error detail (e.g. an RFC 9457 problem document).
	Body string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("extclient: status %d: %s", e.Status, e.Body)
}

func (e *APIError) Unwrap() error { return ErrStatus }

// ServiceOptions configures a single [Client] (one upstream host). Every
// field has a sensible default; only BaseURL is effectively required.
type ServiceOptions struct {
	// BaseURL is the upstream origin (e.g. "https://api.example.com").
	// Request paths passed to the helpers are resolved against it.
	BaseURL string `koanf:"base_url"`

	// Bearer, when set, is sent as "Authorization: Bearer <Bearer>".
	// Mutually exclusive with AuthHeader; Bearer wins if both are set.
	Bearer string `koanf:"bearer"`

	// AuthHeader sets an arbitrary auth header (name → value), e.g.
	// {"X-API-Key": "..."}. Ignored for the Authorization slot if Bearer
	// is also set.
	AuthHeader map[string]string `koanf:"auth_header"`

	// Headers are default headers applied to every request (e.g. Accept,
	// User-Agent). Per-request headers override these.
	Headers map[string]string `koanf:"headers"`

	// Timeout caps a single request (default 30s, inherited from
	// httpx/client when non-positive).
	Timeout time.Duration `koanf:"timeout"`

	// CacheTTL, when > 0, enables a response cache for GET requests keyed by
	// full URL plus effective request headers. Requires a *memory.Cache passed
	// to [New] via [WithCache]; without one, caching is silently disabled.
	CacheTTL time.Duration `koanf:"cache_ttl"`

	// Retry mirrors httpx/client retry policy. Zero disables retries.
	Retry client.RetryOptions `koanf:"retry"`

	// Breaker mirrors httpx/client circuit-breaker policy. Zero disables it.
	Breaker client.BreakerOptions `koanf:"breaker"`

	// Name identifies this client in breaker logs + OTel spans. Defaults to
	// the BaseURL host.
	Name string `koanf:"name"`
}

// Option mutates a [Client] at construction time (functional-options style),
// for dependencies that don't belong in koanf config (cache pool, logger).
type Option func(*Client)

// WithCache attaches a shared cache pool used to cache GET responses when
// ServiceOptions.CacheTTL > 0. Pass the app's *memory.Cache.
func WithCache(c *memory.Cache) Option {
	return func(cl *Client) { cl.cache = c }
}

// WithLogger overrides the logger used for the underlying transport.
func WithLogger(l *slog.Logger) Option {
	return func(cl *Client) { cl.logger = l }
}

// Client is a configured client for a single upstream host. Safe for
// concurrent use. Build one with [New].
type Client struct {
	http   *http.Client
	base   *url.URL
	opts   ServiceOptions
	cache  *memory.Cache
	logger *slog.Logger
	name   string
}

// New builds a [Client] from opts. It validates BaseURL, clones caller-owned
// header maps, and constructs the underlying transport stack.
func New(opts ServiceOptions, options ...Option) (*Client, error) {
	if strings.TrimSpace(opts.BaseURL) == "" {
		return nil, errors.New("extclient: BaseURL is required")
	}
	base, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("extclient: parse BaseURL %q: %w", opts.BaseURL, err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("extclient: BaseURL %q must be absolute (scheme + host)", opts.BaseURL)
	}
	if base.User != nil {
		return nil, fmt.Errorf("extclient: BaseURL %q must not contain userinfo", opts.BaseURL)
	}
	opts.AuthHeader = maps.Clone(opts.AuthHeader)
	opts.Headers = maps.Clone(opts.Headers)

	name := opts.Name
	if name == "" {
		name = "extclient:" + base.Host
	}

	cl := &Client{base: base, opts: opts, name: name}
	for _, o := range options {
		if o != nil {
			o(cl)
		}
	}
	if cl.logger == nil {
		cl.logger = slog.Default()
	}

	cl.http = client.New(client.Options{
		Name:    name,
		Timeout: opts.Timeout,
		Retry:   opts.Retry,
		Breaker: opts.Breaker,
		Logger:  cl.logger,
	})
	cl.http.CheckRedirect = cl.checkRedirect
	return cl, nil
}

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("extclient: stopped after 10 redirects")
	}
	if !strings.EqualFold(req.URL.Scheme, c.base.Scheme) ||
		!strings.EqualFold(req.URL.Host, c.base.Host) {
		return fmt.Errorf("extclient: redirect escapes configured upstream to %q", req.URL.Redacted())
	}
	return nil
}

// HTTPClient exposes the underlying *http.Client for callers that need raw
// access (streaming, non-JSON bodies, custom decoding).
func (c *Client) HTTPClient() *http.Client { return c.http }

// resolve joins a request path against the base URL.
func (c *Client) resolve(path string) (string, error) {
	ref, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("extclient: parse path %q: %w", path, err)
	}
	if ref.IsAbs() || ref.Host != "" || ref.User != nil {
		return "", fmt.Errorf("extclient: path %q must be relative to configured upstream", path)
	}
	resolved := c.base.ResolveReference(ref)
	if !strings.EqualFold(resolved.Scheme, c.base.Scheme) || !strings.EqualFold(resolved.Host, c.base.Host) {
		return "", fmt.Errorf("extclient: path %q escapes configured upstream", path)
	}
	return resolved.String(), nil
}

// requestHeaders builds the exact effective header set for one request.
func (c *Client) requestHeaders(hasBody bool, perRequest map[string]string) http.Header {
	headers := make(http.Header, len(c.opts.AuthHeader)+len(c.opts.Headers)+len(perRequest)+2)
	headers.Set("Accept", "application/json")
	if hasBody {
		headers.Set("Content-Type", "application/json")
	}
	for k, v := range c.opts.Headers {
		headers.Set(k, v)
	}
	for k, v := range c.opts.AuthHeader {
		headers.Set(k, v)
	}
	if c.opts.Bearer != "" {
		headers.Set("Authorization", "Bearer "+c.opts.Bearer)
	}
	for k, v := range perRequest {
		headers.Set(k, v)
	}
	return headers
}

// doJSON performs the request and returns the raw 2xx body (or an *APIError).
// It centralises request building, header application, status checking, and
// bounded body reads so the generic helpers stay thin.
func (c *Client) doJSON(
	ctx context.Context, method, path string, body []byte, headers map[string]string,
) (data []byte, err error) {
	rawURL, err := c.resolve(path)
	if err != nil {
		return nil, err
	}

	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("extclient: build request: %w", err)
	}
	req.Header = c.requestHeaders(body != nil, headers)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("extclient: do %s %s: %w", method, rawURL, err)
	}
	defer func() {
		gerr.CloseInto(resp.Body, &err, "extclient: close response body")
	}()

	data, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("extclient: read body: %w", err)
	}
	overflow := len(data) > maxResponseBytes
	if overflow {
		data = data[:maxResponseBytes]
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.logger.WarnContext(
			ctx, "extclient: non-2xx response",
			slog.String("name", c.name),
			slog.String("method", method),
			slog.Int("status", resp.StatusCode),
		)
		return nil, &APIError{Status: resp.StatusCode, Body: truncate(string(data), 512)}
	}
	if overflow {
		return nil, fmt.Errorf(
			"extclient: response body exceeds %d bytes: %w",
			maxResponseBytes,
			ErrResponseTooLarge,
		)
	}
	return data, nil
}

// truncate caps s to n bytes, appending an ellipsis marker when cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// decode unmarshals data into a fresh T. An empty body yields the zero value
// (e.g. a 204 No Content).
func decode[T any](data []byte) (T, error) {
	var out T
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("extclient: decode response: %w", err)
	}
	return out, nil
}

// Get issues a GET to path (resolved against the client's BaseURL) and decodes
// the JSON response into T. When the client's CacheTTL > 0 and a cache pool is
// attached, successful responses are cached by URL and effective headers.
func Get[T any](ctx context.Context, c *Client, path string, headers map[string]string) (T, error) {
	var zero T
	cached, hit, key := c.cacheGet(path, headers)
	if hit {
		return decode[T](cached)
	}
	data, err := c.doJSON(ctx, http.MethodGet, path, nil, headers)
	if err != nil {
		return zero, err
	}
	c.cacheSet(key, data) // no-op when caching is disabled (key == "")
	return decode[T](data)
}

// Post marshals body to JSON, issues a POST, and decodes the response into T.
// A nil body sends no request body.
func Post[T any](ctx context.Context, c *Client, path string, body any, headers map[string]string) (T, error) {
	return send[T](ctx, c, http.MethodPost, path, body, headers)
}

// Put marshals body to JSON, issues a PUT, and decodes the response into T.
func Put[T any](ctx context.Context, c *Client, path string, body any, headers map[string]string) (T, error) {
	return send[T](ctx, c, http.MethodPut, path, body, headers)
}

// Delete issues a DELETE and decodes any response body into T.
func Delete[T any](ctx context.Context, c *Client, path string, headers map[string]string) (T, error) {
	return send[T](ctx, c, http.MethodDelete, path, nil, headers)
}

// send marshals body (if any) and performs a mutating request, decoding the
// response into T. Shared by Post/Put/Delete.
func send[T any](
	ctx context.Context, c *Client, method, path string, body any, headers map[string]string,
) (T, error) {
	var zero T
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return zero, fmt.Errorf("extclient: encode request body: %w", err)
		}
		raw = b
	}
	data, err := c.doJSON(ctx, method, path, raw, headers)
	if err != nil {
		return zero, err
	}
	return decode[T](data)
}

// cacheGet returns the cached body for path's resolved URL. The third return
// value is the cache key when caching is enabled (and empty when disabled), so
// callers can distinguish "caching off" from "cache miss".
func (c *Client) cacheGet(path string, headers map[string]string) ([]byte, bool, string) {
	if c.cache == nil || c.opts.CacheTTL <= 0 {
		return nil, false, ""
	}
	rawURL, err := c.resolve(path)
	if err != nil {
		return nil, false, ""
	}
	key := cacheKey(rawURL, c.requestHeaders(false, headers))
	store := memory.Typed[string, []byte](c.cache, "extclient:"+c.name)
	if v, ok := store.Get(key); ok {
		return v, true, key
	}
	return nil, false, key
}

// cacheSet stores data under key with the client's CacheTTL. It re-applies the
// per-entry TTL because the shared pool's default TTL may differ.
func (c *Client) cacheSet(key string, data []byte) {
	if c.cache == nil || c.opts.CacheTTL <= 0 || key == "" {
		return
	}
	store := memory.Typed[string, []byte](c.cache, "extclient:"+c.name)
	store.Set(key, data)
	store.SetExpiresAfter(key, c.opts.CacheTTL)
}

func cacheKey(rawURL string, headers http.Header) string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var material strings.Builder
	for _, key := range keys {
		material.WriteString(strconv.Quote(key))
		values := slices.Clone(headers.Values(key))
		sort.Strings(values)
		for _, value := range values {
			material.WriteString(strconv.Quote(value))
		}
	}
	digest := sha256.Sum256([]byte(material.String()))
	return rawURL + "#headers=" + hex.EncodeToString(digest[:])
}
