// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package torrent

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNewClient_RejectsNilLogger(t *testing.T) {
	t.Parallel()
	c, err := newClient(nil, Options{
		Backend: backendTransmission,
		Timeout: time.Second,
		Transmission: TransmissionOptions{
			URL: "http://127.0.0.1:9091/transmission/rpc",
		},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "logger") {
		t.Fatalf("newClient error = %v; want logger dependency error", err)
	}
	if c != nil {
		t.Fatal("newClient returned a client with nil logger")
	}
}

// TestNewHTTPClient_ownsTransport pins that every backend client gets its own
// transport, never the shared http.DefaultTransport pool (#703).
func TestNewHTTPClient_ownsTransport(t *testing.T) {
	t.Parallel()
	for _, insecure := range []bool{false, true} {
		first := newHTTPClient(time.Second, insecure)
		tr, ok := first.Transport.(*http.Transport)
		if !ok || first.Transport == http.DefaultTransport {
			t.Fatalf("insecure=%v: transport = %T shared=%v, want a private *http.Transport",
				insecure, first.Transport, first.Transport == http.DefaultTransport)
		}
		if got := tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify; got != insecure {
			t.Errorf("insecure=%v: InsecureSkipVerify = %v", insecure, got)
		}
		if second := newHTTPClient(time.Second, insecure); second.Transport == first.Transport {
			t.Fatalf("insecure=%v: two clients share one transport", insecure)
		}
	}
}

// TestNewHTTPClient_timeout covers the explicit and zero-timeout boundaries.
func TestNewHTTPClient_timeout(t *testing.T) {
	t.Parallel()
	if got := newHTTPClient(time.Second, false).Timeout; got != time.Second {
		t.Errorf("explicit Timeout = %v, want 1s", got)
	}
	if got := newHTTPClient(0, false).Timeout; got != defaultTimeout {
		t.Errorf("zero Timeout = %v, want %v", got, defaultTimeout)
	}
}
