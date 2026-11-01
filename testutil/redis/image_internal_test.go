// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package redis

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/golusoris/golusoris/internal/testimages"
)

type optionsImageCase struct {
	name    string
	image   string
	want    string
	refused bool
}

// checkOptionsImageRefused verifies that invalid or mutable image references are rejected.
func checkOptionsImageRefused(t *testing.T, got string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("image() = %q, want a mutable reference refused", got)
	}
	if !strings.Contains(err.Error(), "testutil/redis: validate image") {
		t.Fatalf("image() error = %v, want it to name the helper and the validation", err)
	}
}

// checkOptionsImageValid verifies that valid image references resolve to the expected tag and digest.
func checkOptionsImageValid(t *testing.T, got, want string, err error) {
	t.Helper()
	if err != nil || got != want {
		t.Fatalf("image() = %q, %v; want %q", got, err, want)
	}
}

// checkOptionsImageCase evaluates one test case for options image resolution.
func checkOptionsImageCase(t *testing.T, tc optionsImageCase) {
	t.Helper()
	got, err := Options{Image: tc.image}.image()
	if tc.refused {
		checkOptionsImageRefused(t, got, err)
		return
	}
	checkOptionsImageValid(t, got, tc.want, err)
}

func TestOptionsImage(t *testing.T) {
	t.Parallel()
	const pinned = "redis:7-alpine@sha256:858f009f9709ce576febc734aa78b8f6d624b82571f9ddb6bda4377c833b3499"
	cases := []optionsImageCase{
		{name: "empty uses the repository pin", want: testimages.Redis},
		{name: "explicit immutable reference", image: pinned, want: pinned},
		{name: "tag without digest", image: "redis:7-alpine", refused: true},
		{name: "digest without tag", image: "redis@sha256:" + strings.Repeat("a", 64), refused: true},
		{name: "short digest", image: "redis:7-alpine@sha256:abc", refused: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkOptionsImageCase(t, tc)
		})
	}
}

// TestAddrWithExplicitImage boots through the option path with an explicit reference.
func TestAddrWithExplicitImage(t *testing.T) {
	t.Parallel()
	addr := AddrWith(t, Options{Image: testimages.Redis})
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	if err = conn.Close(); err != nil {
		t.Fatalf("close connection: %v", err)
	}
}
