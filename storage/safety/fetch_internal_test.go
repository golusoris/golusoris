// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package safety

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestNewFetcher_NilLoggerAllowsPrivateMode(t *testing.T) {
	t.Parallel()
	f, err := newFetcher(Options{Fetch: FetchOptions{AllowPrivate: true}}, nil)
	if err != nil {
		t.Fatalf("newFetcher: %v", err)
	}
	if f == nil {
		t.Fatal("newFetcher returned nil")
	}
}

func TestCappedBodyRejectsSentinelReturnedWithEOF(t *testing.T) {
	t.Parallel()
	body := &cappedBody{
		rc:        &dataEOFReadCloser{data: []byte("12345")},
		remaining: 5,
		cancel:    func() {},
	}
	buf := make([]byte, 5)
	n, err := body.Read(buf)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Read error = %v; want ErrTooLarge", err)
	}
	if n != 4 || string(buf[:n]) != "1234" {
		t.Fatalf("Read = (%d, %q); want (4, %q)", n, buf[:n], "1234")
	}
}

type dataEOFReadCloser struct {
	data []byte
}

func (r *dataEOFReadCloser) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, io.EOF
}

func (*dataEOFReadCloser) Close() error { return nil }

// TestFetch_RedirectToPrivate confirms the SSRF guard re-runs on a redirect hop:
// only the loopback first hop bypasses it, the metadata-IP hop must be blocked.
func TestFetch_RedirectToPrivate(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()
	first, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}

	built, err := newFetcher(Options{Fetch: FetchOptions{
		MaxBytes: 1 << 20, Timeout: 5 * time.Second,
		AllowedSchemes: []string{"http"}, MaxRedirects: 3,
	}}, nil)
	if err != nil {
		t.Fatalf("newFetcher: %v", err)
	}
	f, ok := built.(*fetcher)
	if !ok {
		t.Fatalf("newFetcher returned %T; want *fetcher", built)
	}
	transport, ok := f.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport is %T; want *http.Transport", f.client.Transport)
	}
	guarded := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == first.Host {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
		return guarded(ctx, network, addr)
	}

	_, _, err = f.Fetch(t.Context(), srv.URL)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("redirect to metadata IP error = %v; want ErrBlockedAddress", err)
	}
}
