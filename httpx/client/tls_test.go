// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package client_test

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
	"github.com/golusoris/golusoris/httpx/client"
)

// mtlsServer starts an HTTPS server that requires a client certificate from
// ca and returns its URL plus a reloader holding a CA-issued client leaf.
func mtlsServer(t *testing.T) (string, *tlsx.Reloader) {
	t.Helper()
	ca := tlsxtest.NewCA(t)
	srvReloader, err := tlsx.NewReloader(ca.WriteFiles(t, t.TempDir(), ca.Server(t, "127.0.0.1")), tlsx.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.TLS.PeerCertificates[0].Subject.CommonName)
	}))
	ts.TLS = srvReloader.ServerConfig(tls.RequireAndVerifyClientCert)
	ts.StartTLS()
	t.Cleanup(ts.Close)

	cli, err := tlsx.NewReloader(ca.WriteFiles(t, t.TempDir(), ca.Client(t, "billing")), tlsx.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return ts.URL, cli
}

func getBody(t *testing.T, c *http.Client, url string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := client.ReadAllBounded(resp.Body, 1<<10)
	return string(body), err
}

func TestNewTLSConfigPresentsClientCertificate(t *testing.T) {
	t.Parallel()
	url, cli := mtlsServer(t)

	c := client.New(client.Options{TLSConfig: cli.ClientConfig(""), Retry: client.RetryOptions{Max: 1, Wait: time.Millisecond}})
	got, err := getBody(t, c, url)
	if err != nil || got != "billing" {
		t.Fatalf("mTLS GET = (%q, %v), want billing", got, err)
	}

	// Default transport: system roots reject the test CA.
	if _, err := getBody(t, client.New(client.Options{}), url); err == nil {
		t.Fatal("GET without TLSConfig succeeded against a private CA")
	}
}

// countingTransport answers 503 until failures runs out, then 200.
type countingTransport struct {
	calls    atomic.Int32
	failures int32
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	status := http.StatusOK
	if c.calls.Add(1) <= c.failures {
		status = http.StatusServiceUnavailable
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     http.Header{},
		Request:    req,
	}, nil
}

func TestNewTransportOverrideKeepsRetry(t *testing.T) {
	t.Parallel()
	tr := &countingTransport{failures: 2}
	c := client.New(client.Options{
		Transport: tr,
		// Boundary: Transport wins over TLSConfig.
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13},
		Retry:     client.RetryOptions{Max: 2, Wait: time.Millisecond, MaxWait: time.Millisecond},
	})
	got, err := getBody(t, c, "http://upstream.invalid/")
	if err != nil || got != "ok" {
		t.Fatalf("GET = (%q, %v)", got, err)
	}
	if n := tr.calls.Load(); n != 3 {
		t.Fatalf("transport calls = %d, want 3 (two retries)", n)
	}
}

func TestNewTransportOverrideWithoutRetry(t *testing.T) {
	t.Parallel()
	tr := &countingTransport{failures: 1}
	c := client.New(client.Options{Transport: tr})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://upstream.invalid/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || tr.calls.Load() != 1 {
		t.Fatalf("status = %d calls = %d, want 503 after one call", resp.StatusCode, tr.calls.Load())
	}
}
