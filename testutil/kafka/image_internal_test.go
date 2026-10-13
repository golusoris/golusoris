// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package kafka

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/internal/testimages"
)

func TestOptionsImage(t *testing.T) {
	t.Parallel()
	const pinned = "redpandadata/redpanda:v24.3.18@sha256:5c6bdbaf30c97de1ebc2ad417b7d02e0bdb5bf88e243bd3079d74bfde9c7754f"
	cases := []struct {
		name    string
		image   string
		want    string
		refused bool
	}{
		{name: "empty uses the repository pin", want: testimages.Redpanda},
		{name: "explicit immutable reference", image: pinned, want: pinned},
		{name: "tag without digest", image: "redpandadata/redpanda:v24.3.18", refused: true},
		{name: "digest without tag", image: "redpandadata/redpanda@sha256:" + strings.Repeat("a", 64), refused: true},
		{name: "short digest", image: "redpandadata/redpanda:v24.3.18@sha256:abc", refused: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Options{Image: tc.image}.image()
			if tc.refused {
				if err == nil {
					t.Fatalf("image() = %q, want a mutable reference refused", got)
				}
				if !strings.Contains(err.Error(), "testutil/kafka: validate image") {
					t.Fatalf("image() error = %v, want it to name the helper and the validation", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("image() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// TestAddrWithExplicitImage boots through the option path with an explicit reference.
func TestAddrWithExplicitImage(t *testing.T) {
	t.Parallel()
	addr := AddrWith(t, Options{Image: testimages.Redpanda})
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	if err = conn.Close(); err != nil {
		t.Fatalf("close connection: %v", err)
	}
}
