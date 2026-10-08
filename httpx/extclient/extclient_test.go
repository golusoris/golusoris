// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package extclient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/httpx/client"
	"github.com/golusoris/golusoris/httpx/extclient"
)

type user struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(`{"id":"ok"}`)),
		Request:    req,
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestNewValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		baseURL string
		wantErr bool
	}{
		{name: "empty", baseURL: "", wantErr: true},
		{name: "relative", baseURL: "/foo", wantErr: true},
		{name: "no scheme", baseURL: "api.example.com", wantErr: true},
		{name: "absolute", baseURL: "https://api.example.com", wantErr: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := extclient.New(extclient.ServiceOptions{BaseURL: tt.baseURL})
			if (err != nil) != tt.wantErr {
				t.Fatalf("New(%q) err = %v, wantErr = %v", tt.baseURL, err, tt.wantErr)
			}
		})
	}
}

func TestNewAppliesFiniteTimeoutPolicy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "negative defaults", timeout: -time.Nanosecond, want: 30 * time.Second},
		{name: "zero defaults", timeout: 0, want: 30 * time.Second},
		{name: "positive preserved", timeout: time.Nanosecond, want: time.Nanosecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			c, err := extclient.New(extclient.ServiceOptions{
				BaseURL: "https://example.test",
				Timeout: test.timeout,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if got := c.HTTPClient().Timeout; got != test.want {
				t.Fatalf("Timeout = %v, want %v", got, test.want)
			}
		})
	}
}

func TestGetDecodesJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/42" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		_ = json.NewEncoder(w).Encode(user{ID: "42", Name: "ada"})
	}))
	defer srv.Close()

	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := extclient.Get[user](context.Background(), c, "/users/42", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != (user{ID: "42", Name: "ada"}) {
		t.Errorf("got = %+v", got)
	}
}

func TestRequestPathCannotEscapeConfiguredUpstream(t *testing.T) {
	t.Parallel()
	var baseHits atomic.Int32
	base := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		baseHits.Add(1)
		_ = json.NewEncoder(w).Encode(user{})
	}))
	defer base.Close()
	var attackerHits atomic.Int32
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attackerHits.Add(1)
		_ = json.NewEncoder(w).Encode(user{})
	}))
	defer attacker.Close()

	c, err := extclient.New(extclient.ServiceOptions{BaseURL: base.URL, Bearer: "secret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	attackerURL, err := url.Parse(attacker.URL)
	if err != nil {
		t.Fatalf("parse attacker URL: %v", err)
	}
	paths := []string{attacker.URL + "/steal", "//" + attackerURL.Host + "/steal"}
	for _, path := range paths {
		if _, requestErr := extclient.Get[user](context.Background(), c, path, nil); requestErr == nil {
			t.Errorf("Get(%q) succeeded", path)
		}
	}
	if attackerHits.Load() != 0 || baseHits.Load() != 0 {
		t.Fatalf("requests escaped validation: attacker=%d base=%d", attackerHits.Load(), baseHits.Load())
	}
}

func TestCrossOriginRedirectsDoNotLeakConfiguredCredentials(t *testing.T) {
	t.Parallel()
	for _, status := range []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			var attackerHits atomic.Int32
			attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attackerHits.Add(1)
				t.Errorf("cross-origin redirect reached attacker with Authorization=%q X-Api-Key=%q", r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"))
				w.WriteHeader(http.StatusNoContent)
			}))
			defer attacker.Close()

			base := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", attacker.URL+"/steal")
				w.WriteHeader(status)
			}))
			defer base.Close()

			c, err := extclient.New(extclient.ServiceOptions{
				BaseURL:    base.URL,
				Bearer:     "bearer-credential",
				AuthHeader: map[string]string{"X-API-Key": "custom-credential"},
				Retry:      client.RetryOptions{Max: 1},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err = extclient.Post[user](
				t.Context(),
				c,
				"/redirect",
				user{Name: "payload"},
				map[string]string{"Idempotency-Key": "redirect-test"},
			); err == nil {
				t.Fatal("cross-origin redirect succeeded")
			}
			if got := attackerHits.Load(); got != 0 {
				t.Fatalf("attacker received %d requests, want 0", got)
			}
		})
	}
}

func TestSameOriginRedirectRetainsConfiguredCredentials(t *testing.T) {
	t.Parallel()
	var finalHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		finalHits.Add(1)
		if got := r.Header.Get("Authorization"); got != "Bearer bearer-credential" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Api-Key"); got != "custom-credential" {
			t.Errorf("X-API-Key = %q", got)
		}
		_ = json.NewEncoder(w).Encode(user{ID: "ok"})
	}))
	defer srv.Close()

	c, err := extclient.New(extclient.ServiceOptions{
		BaseURL:    srv.URL,
		Bearer:     "bearer-credential",
		AuthHeader: map[string]string{"X-API-Key": "custom-credential"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err = extclient.Get[user](t.Context(), c, "/redirect", nil); err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	if got := finalHits.Load(); got != 1 {
		t.Fatalf("final hits = %d, want 1", got)
	}
}

func TestAuthAndHeaders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		opts       extclient.ServiceOptions
		perRequest map[string]string
		wantAuth   string
		wantHeader map[string]string
	}{
		{
			name:     "bearer",
			opts:     extclient.ServiceOptions{Bearer: "tok123"},
			wantAuth: "Bearer tok123",
		},
		{
			name:       "api key header",
			opts:       extclient.ServiceOptions{AuthHeader: map[string]string{"X-API-Key": "secret"}},
			wantHeader: map[string]string{"X-Api-Key": "secret"},
		},
		{
			name: "bearer wins over auth header Authorization",
			opts: extclient.ServiceOptions{
				Bearer:     "tok123",
				AuthHeader: map[string]string{"Authorization": "Basic nope"},
			},
			wantAuth: "Bearer tok123",
		},
		{
			name: "bearer wins over default Authorization header",
			opts: extclient.ServiceOptions{
				Bearer:  "tok123",
				Headers: map[string]string{"Authorization": "Basic nope"},
			},
			wantAuth: "Bearer tok123",
		},
		{
			name:       "per-request overrides default",
			opts:       extclient.ServiceOptions{Headers: map[string]string{"X-Trace": "default"}},
			perRequest: map[string]string{"X-Trace": "override"},
			wantHeader: map[string]string{"X-Trace": "override"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.wantAuth != "" && r.Header.Get("Authorization") != tt.wantAuth {
					t.Errorf("Authorization = %q, want %q", r.Header.Get("Authorization"), tt.wantAuth)
				}
				for k, v := range tt.wantHeader {
					if got := r.Header.Get(k); got != v {
						t.Errorf("header %s = %q, want %q", k, got, v)
					}
				}
				_ = json.NewEncoder(w).Encode(user{ID: "1"})
			}))
			defer srv.Close()

			opts := tt.opts
			opts.BaseURL = srv.URL
			c, err := extclient.New(opts)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := extclient.Get[user](context.Background(), c, "/x", tt.perRequest); err != nil {
				t.Fatalf("Get: %v", err)
			}
		})
	}
}

func TestNewClonesCallerHeaderMaps(t *testing.T) {
	t.Parallel()
	headers := map[string]string{"X-Default": "original"}
	authHeader := map[string]string{"X-API-Key": "secret"}
	c, err := extclient.New(extclient.ServiceOptions{
		BaseURL:    "https://example.test",
		Headers:    headers,
		AuthHeader: authHeader,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	headers["X-Default"] = "mutated"
	headers["X-Added"] = "late"
	authHeader["X-API-Key"] = "mutated"
	authHeader["X-Auth-Added"] = "late"

	var received http.Header
	c.HTTPClient().Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		received = req.Header.Clone()
		return jsonResponse(req), nil
	})
	if _, err = extclient.Get[user](context.Background(), c, "/", nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := received.Get("X-Default"); got != "original" {
		t.Errorf("X-Default = %q, want original", got)
	}
	if got := received.Get("X-Api-Key"); got != "secret" {
		t.Errorf("X-API-Key = %q, want secret", got)
	}
	if got := received.Get("X-Added"); got != "" {
		t.Errorf("X-Added = %q, want empty", got)
	}
	if got := received.Get("X-Auth-Added"); got != "" {
		t.Errorf("X-Auth-Added = %q, want empty", got)
	}
}

func TestClientDoesNotRaceCallerHeaderMapMutation(t *testing.T) {
	t.Parallel()
	headers := map[string]string{"X-Default": "original"}
	authHeader := map[string]string{"X-API-Key": "secret"}
	c, err := extclient.New(extclient.ServiceOptions{
		BaseURL:    "https://example.test",
		Headers:    headers,
		AuthHeader: authHeader,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.HTTPClient().Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(req), nil
	})

	start := make(chan struct{})
	var writers sync.WaitGroup
	writers.Go(func() {
		<-start
		for i := range 10_000 {
			headers["X-Default"] = fmt.Sprintf("changed-%d", i)
			authHeader["X-API-Key"] = fmt.Sprintf("changed-%d", i)
		}
	})
	close(start)
	for range 100 {
		if _, err = extclient.Get[user](context.Background(), c, "/", nil); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	writers.Wait()
}

func TestPostSendsBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var in user
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Errorf("decode: %v", err)
		}
		in.ID = "created"
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(in)
	}))
	defer srv.Close()

	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := extclient.Post[user](context.Background(), c, "/users", user{Name: "grace"}, nil)
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got.ID != "created" || got.Name != "grace" {
		t.Errorf("got = %+v", got)
	}
}

func TestNon2xxReturnsAPIError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, `{"detail":"nope"}`)
	}))
	defer srv.Close()

	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = extclient.Get[user](context.Background(), c, "/x", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, extclient.ErrStatus) {
		t.Errorf("errors.Is(ErrStatus) = false: %v", err)
	}
	var apiErr *extclient.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(*APIError) = false: %v", err)
	}
	if apiErr.Status != http.StatusTeapot {
		t.Errorf("Status = %d", apiErr.Status)
	}
	if apiErr.Body != `{"detail":"nope"}` {
		t.Errorf("Body = %q", apiErr.Body)
	}
}

func TestDeleteEmptyBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// 204 with empty body decodes to the zero value without error.
	got, err := extclient.Delete[user](context.Background(), c, "/users/1", nil)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got != (user{}) {
		t.Errorf("got = %+v, want zero", got)
	}
}

func TestGetCachesByURL(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(user{ID: "cached"})
	}))
	defer srv.Close()

	pool, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}
	c, err := extclient.New(
		extclient.ServiceOptions{BaseURL: srv.URL, CacheTTL: time.Minute},
		extclient.WithCache(pool),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for range 3 {
		got, gErr := extclient.Get[user](context.Background(), c, "/thing", nil)
		if gErr != nil {
			t.Fatalf("Get: %v", gErr)
		}
		if got.ID != "cached" {
			t.Errorf("got = %+v", got)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("upstream hits = %d, want 1 (cache should serve the rest)", n)
	}
}

func TestGetCacheIsolatesCredentialsAndCallerHeaders(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(user{
			ID: r.Header.Get("Authorization") + "|" + r.Header.Get("X-Tenant"),
		})
	}))
	defer srv.Close()

	pool, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}
	newClient := func(token string) *extclient.Client {
		t.Helper()
		c, newErr := extclient.New(
			extclient.ServiceOptions{
				BaseURL:  srv.URL,
				Bearer:   token,
				CacheTTL: time.Minute,
				Name:     "shared-cache-name",
			},
			extclient.WithCache(pool),
		)
		if newErr != nil {
			t.Fatalf("New: %v", newErr)
		}
		return c
	}
	alpha, beta := newClient("alpha"), newClient("beta")
	requests := []struct {
		client *extclient.Client
		tenant string
		wantID string
	}{
		{client: alpha, tenant: "one", wantID: "Bearer alpha|one"},
		{client: beta, tenant: "one", wantID: "Bearer beta|one"},
		{client: alpha, tenant: "two", wantID: "Bearer alpha|two"},
		{client: alpha, tenant: "one", wantID: "Bearer alpha|one"},
		{client: beta, tenant: "one", wantID: "Bearer beta|one"},
	}
	for _, request := range requests {
		got, getErr := extclient.Get[user](
			context.Background(),
			request.client,
			"/thing",
			map[string]string{"X-Tenant": request.tenant},
		)
		if getErr != nil {
			t.Fatalf("Get: %v", getErr)
		}
		if got.ID != request.wantID {
			t.Errorf("response ID = %q, want %q", got.ID, request.wantID)
		}
	}
	if hits.Load() != 3 {
		t.Fatalf("upstream hits = %d, want 3 isolated variants", hits.Load())
	}
}

func TestGetRejectsSuccessfulBodyOverflow(t *testing.T) {
	t.Parallel()
	const responseLimit = 8 << 20
	jsonBody := func(size int) []byte {
		prefix, suffix := []byte(`{"id":"`), []byte(`"}`)
		body := make([]byte, 0, size)
		body = append(body, prefix...)
		body = append(body, bytes.Repeat([]byte("x"), size-len(prefix)-len(suffix))...)
		return append(body, suffix...)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		size := responseLimit
		if r.URL.Path == "/overflow" {
			size++
		}
		_, _ = w.Write(jsonBody(size))
	}))
	defer srv.Close()
	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	exact, err := extclient.Get[user](context.Background(), c, "/exact", nil)
	if err != nil {
		t.Fatalf("Get(exact): %v", err)
	}
	if len(exact.ID) != responseLimit-len(`{"id":"`)-len(`"}`) {
		t.Fatalf("exact response ID bytes = %d", len(exact.ID))
	}
	_, err = extclient.Get[user](context.Background(), c, "/overflow", nil)
	if !errors.Is(err, extclient.ErrResponseTooLarge) {
		t.Fatalf("Get() error = %v, want response-body limit error", err)
	}
}

func TestNoCacheWithoutPool(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_ = json.NewEncoder(w).Encode(user{ID: "x"})
	}))
	defer srv.Close()

	// CacheTTL set but no pool attached -> caching silently disabled.
	c, err := extclient.New(extclient.ServiceOptions{BaseURL: srv.URL, CacheTTL: time.Minute})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for range 2 {
		if _, gErr := extclient.Get[user](context.Background(), c, "/y", nil); gErr != nil {
			t.Fatalf("Get: %v", gErr)
		}
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("upstream hits = %d, want 2 (no caching without pool)", n)
	}
}

func TestRegistryLookup(t *testing.T) {
	t.Parallel()
	opts := extclient.Options{
		Services: map[string]extclient.ServiceOptions{
			"github": {BaseURL: "https://api.github.com"},
			"stripe": {BaseURL: "https://api.stripe.com"},
		},
	}
	reg, err := extclient.NewRegistryForTest(opts, discardLogger(), nil)
	if err != nil {
		t.Fatalf("NewRegistryForTest: %v", err)
	}
	if _, err := reg.Client("github"); err != nil {
		t.Errorf("Client(github): %v", err)
	}
	if _, err := reg.Client("missing"); err == nil {
		t.Error("Client(missing) = nil err, want error")
	}
	names := reg.Names()
	if len(names) != 2 || names[0] != "github" || names[1] != "stripe" {
		t.Errorf("Names() = %v", names)
	}
}

func TestRegistryRejectsBadService(t *testing.T) {
	t.Parallel()
	opts := extclient.Options{
		Services: map[string]extclient.ServiceOptions{
			"broken": {BaseURL: ""},
		},
	}
	if _, err := extclient.NewRegistryForTest(opts, discardLogger(), nil); err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
}

func TestNewIgnoresNilOption(t *testing.T) {
	t.Parallel()
	client, err := extclient.New(extclient.ServiceOptions{BaseURL: "https://example.test"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client == nil {
		t.Fatal("New returned nil client")
	}
}
