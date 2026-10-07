// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package torrent

import (
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
