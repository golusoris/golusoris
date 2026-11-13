// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/bartventer/httpcache"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/twotier"
)

func BenchmarkCacheHit(b *testing.B) { benchmarkHit(b, false) }

func BenchmarkEngineHit(b *testing.B) { benchmarkHit(b, true) }

func benchmarkHit(b *testing.B, engine bool) {
	b.Helper()
	logger := slog.New(slog.DiscardHandler)
	l1, err := memory.NewForTest(1000, 0)
	require.NoError(b, err)
	b.Cleanup(func() { require.True(b, l1.StopAllGoroutines()) })
	store, err := twotier.New(l1, twotier.Options{L2: twotier.L2None}, logger)
	require.NoError(b, err)
	c, err := New(DefaultOptions(), store, logger)
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, c.Close()) })
	calls := 0
	upstream := transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, "metadata", "Cache-Control", "max-age=86400"), nil
	})
	transport := c.Wrap(upstream)
	if engine {
		transport = httpcache.NewTransport(driverScheme+"://"+c.id, httpcache.WithUpstream(upstream), httpcache.WithLogger(logger))
	}
	req, err := http.NewRequestWithContext(b.Context(), http.MethodGet, "http://provider.test/data?b=2&a=%7e&flag", nil)
	require.NoError(b, err)
	benchmarkRead(b, transport, req, "MISS")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkRead(b, transport, req, "HIT")
	}
	b.StopTimer()
	require.Equal(b, 1, calls)
}

func benchmarkRead(b *testing.B, transport http.RoundTripper, req *http.Request, status string) {
	b.Helper()
	resp, err := transport.RoundTrip(req)
	if err != nil {
		b.Fatal(err)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	if err != nil {
		b.Fatal(err)
	}
	if err = resp.Body.Close(); err != nil {
		b.Fatal(err)
	}
	if got := resp.Header.Get("X-Httpcache-Status"); got != status {
		b.Fatalf("status = %s, want %s", got, status)
	}
}
