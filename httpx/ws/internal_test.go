// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golusoris/golusoris/httpx/middleware"
)

func TestOriginAllowedUsesCanonicalSchemeHostAndPort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		target  string
		origin  string
		allowed []string
		want    bool
	}{
		{name: "missing origin", target: "https://example.com/ws", want: true},
		{name: "same https origin", target: "https://example.com/ws", origin: "https://example.com", want: true},
		{name: "explicit default port", target: "https://example.com/ws", origin: "https://example.com:443", want: true},
		{name: "scheme mismatch", target: "https://example.com/ws", origin: "http://example.com", want: false},
		{name: "port mismatch", target: "https://example.com:8443/ws", origin: "https://example.com", want: false},
		{name: "same IPv4", target: "http://127.0.0.1/ws", origin: "http://127.0.0.1", want: true},
		{name: "same IPv6", target: "https://[2001:db8::1]/ws", origin: "https://[2001:db8::1]", want: true},
		{name: "different IPv6", target: "https://[::1]/ws", origin: "https://[2001:db8::1]", want: false},
		{name: "malformed IPv6", target: "https://[::1]/ws", origin: "https://[::1", want: false},
		{name: "non HTTP scheme", target: "https://example.com/ws", origin: "file://example.com", want: false},
		{name: "origin path", target: "https://example.com/ws", origin: "https://example.com/path", want: false},
		{name: "explicit exact origin", target: "https://example.com/ws", origin: "https://trusted.example:8443", allowed: []string{"https://trusted.example:8443"}, want: true},
		{name: "explicit origin wrong port", target: "https://example.com/ws", origin: "https://trusted.example:9443", allowed: []string{"https://trusted.example:8443"}, want: false},
		{name: "wildcard", target: "https://example.com/ws", origin: "not an origin", allowed: []string{"*"}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if got := originAllowed(request, test.allowed); got != test.want {
				t.Fatalf("originAllowed() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestOriginAllowedUsesTrustedForwardedProto(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		remoteAddr string
		proto      []string
		want       bool
	}{
		{name: "trusted TLS terminator", remoteAddr: "10.1.2.3:4321", proto: []string{"https"}, want: true},
		{name: "untrusted spoof", remoteAddr: "192.0.2.3:4321", proto: []string{"https"}, want: false},
		{name: "trusted duplicate", remoteAddr: "10.1.2.3:4321", proto: []string{"https", "http"}, want: false},
		{name: "trusted comma chain", remoteAddr: "10.1.2.3:4321", proto: []string{"https,http"}, want: false},
		{name: "trusted invalid", remoteAddr: "10.1.2.3:4321", proto: []string{"wss"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			accepted := false
			handler := middleware.TrustProxy(middleware.TrustProxyOptions{
				TrustedCIDRs: []string{"10.0.0.0/8"},
			})(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				accepted = originAllowed(request, nil)
			}))
			request := httptest.NewRequest(http.MethodGet, "http://app.example/ws", nil)
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("Origin", "https://app.example")
			request.Header["X-Forwarded-Proto"] = test.proto
			handler.ServeHTTP(httptest.NewRecorder(), request)
			if accepted != test.want {
				t.Fatalf("originAllowed() = %t, want %t", accepted, test.want)
			}
		})
	}
}

func TestWatchSubscriptionReturnsAfterManualUnsubscribe(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	returned := make(chan struct{})
	go func() {
		watchSubscription(context.Background(), done, func() {})
		close(returned)
	}()
	close(done)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("subscription watcher remained blocked after manual unsubscribe")
	}
}
