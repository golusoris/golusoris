package safety_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/storage/safety"
)

func defaultFetchOpts() safety.FetchOptions {
	return safety.FetchOptions{
		MaxBytes:       1 << 20,
		Timeout:        5 * time.Second,
		AllowedSchemes: []string{"http", "https"},
		MaxRedirects:   3,
	}
}

func newGuardedFetcher(t *testing.T, opts safety.FetchOptions) safety.Fetcher {
	t.Helper()
	f, err := safety.NewFetcherForTest(opts, slog.New(slog.DiscardHandler), clock.NewFake())
	if err != nil {
		t.Fatalf("newFetcher: %v", err)
	}
	return f
}

// TestFetch_BlocksPrivateAndMetadata points the SSRF-guarded fetcher at a
// battery of internal IPs; every one must surface ErrBlockedAddress at dial.
func TestFetch_BlocksPrivateAndMetadata(t *testing.T) {
	t.Parallel()
	f := newGuardedFetcher(t, defaultFetchOpts())
	targets := []string{
		"http://127.0.0.1/",       // loopback v4
		"http://[::1]/",           // loopback v6
		"http://169.254.169.254/", // cloud metadata
		"http://10.0.0.5/",        // RFC1918
		"http://192.168.1.1/",     // RFC1918
		"http://172.16.0.1/",      // RFC1918
		"http://100.64.0.1/",      // CGNAT
		"http://0.0.0.0/",         // unspecified
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			_, _, err := f.Fetch(context.Background(), target)
			if !errors.Is(err, safety.ErrBlockedAddress) {
				t.Fatalf("Fetch(%q) err = %v; want ErrBlockedAddress", target, err)
			}
		})
	}
}

// TestFetch_BlocksHostResolvingPrivate covers DNS-level SSRF: a hostname that
// resolves to a private IP must still be blocked at dial time.
func TestFetch_BlocksHostResolvingPrivate(t *testing.T) {
	t.Parallel()
	f := newGuardedFetcher(t, defaultFetchOpts())
	// localhost resolves to 127.0.0.1 / ::1 — both on the deny list.
	_, _, err := f.Fetch(context.Background(), "http://localhost/")
	if !errors.Is(err, safety.ErrBlockedAddress) {
		t.Fatalf("Fetch(localhost) err = %v; want ErrBlockedAddress", err)
	}
}

func TestFetch_BadScheme(t *testing.T) {
	t.Parallel()
	f := newGuardedFetcher(t, defaultFetchOpts())
	for _, target := range []string{"file:///etc/passwd", "gopher://x/", "ftp://h/"} {
		_, _, err := f.Fetch(context.Background(), target)
		if !errors.Is(err, safety.ErrBadScheme) {
			t.Fatalf("Fetch(%q) err = %v; want ErrBadScheme", target, err)
		}
	}
}

func TestFetch_Success(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "hello world")
	}))
	defer srv.Close()

	// AllowPrivate so the loopback httptest server is reachable.
	opts := defaultFetchOpts()
	opts.AllowPrivate = true
	f := newGuardedFetcher(t, opts)

	body, ctype, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("body = %q, want %q", got, "hello world")
	}
	if !strings.HasPrefix(ctype, "text/plain") {
		t.Fatalf("content type = %q, want text/plain", ctype)
	}
}

func TestFetch_TooLarge(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 4096))
	}))
	defer srv.Close()

	opts := defaultFetchOpts()
	opts.AllowPrivate = true
	opts.MaxBytes = 1024
	f := newGuardedFetcher(t, opts)

	body, _, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer body.Close()
	_, err = io.ReadAll(body)
	if !errors.Is(err, safety.ErrTooLarge) {
		t.Fatalf("read oversized body err = %v; want ErrTooLarge", err)
	}
}

func TestFetch_ExactlyAtCap(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 1024))
	}))
	defer srv.Close()

	opts := defaultFetchOpts()
	opts.AllowPrivate = true
	opts.MaxBytes = 1024
	f := newGuardedFetcher(t, opts)

	body, _, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("body exactly at cap should read clean: %v", err)
	}
	if len(got) != 1024 {
		t.Fatalf("read %d bytes, want 1024", len(got))
	}
}

// TestFetch_RedirectToPrivate confirms a 302 to a private IP is blocked at the
// second dial hop (Control re-runs per hop).
func TestFetch_RedirectToPrivate(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, nil, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	opts := defaultFetchOpts()
	opts.AllowPrivate = true // first hop (loopback) allowed; guard still set for redirects
	f := newGuardedFetcher(t, opts)

	_, _, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("redirect to metadata IP should fail")
	}
}

// TestFetch_TooManyRedirects bounds the redirect chain.
func TestFetch_TooManyRedirects(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()

	opts := defaultFetchOpts()
	opts.AllowPrivate = true
	opts.MaxRedirects = 2
	f := newGuardedFetcher(t, opts)

	_, _, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("redirect loop should error on hop bound")
	}
}

func TestFetch_HostAllowlist(t *testing.T) {
	t.Parallel()
	opts := defaultFetchOpts()
	opts.AllowHosts = []string{"allowed.example"}
	f := newGuardedFetcher(t, opts)

	_, _, err := f.Fetch(context.Background(), "https://blocked.example/")
	if !errors.Is(err, safety.ErrBlockedAddress) {
		t.Fatalf("non-allowlisted host err = %v; want ErrBlockedAddress", err)
	}
}

func TestFetch_BadURL(t *testing.T) {
	t.Parallel()
	f := newGuardedFetcher(t, defaultFetchOpts())
	_, _, err := f.Fetch(context.Background(), "://nonsense")
	if err == nil {
		t.Fatal("unparseable URL should error")
	}
}
