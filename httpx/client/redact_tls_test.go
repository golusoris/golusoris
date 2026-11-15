// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package client_test

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/httpx/client"
)

// retryablehttp skips retries only when the transport error is a bare *tls.CertificateVerificationError.
func TestTLSVerificationFailureNotRetried(t *testing.T) {
	t.Parallel()
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ErrorLog = slog.NewLogLogger(slog.DiscardHandler, slog.LevelError)
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	c := client.New(client.Options{Retry: client.RetryOptions{Max: 2, Wait: time.Millisecond, MaxWait: time.Millisecond}})
	t.Cleanup(c.CloseIdleConnections)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/?api_key=s3cr3t", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		if err = resp.Body.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("untrusted server certificate accepted")
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("connections = %d, want 1: TLS verification failure was retried", got)
	}
}
