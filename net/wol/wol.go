// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package wol sends Wake-on-LAN magic packets over UDP.
//
// Usage:
//
//	err := wol.WakeContext(ctx, "aa:bb:cc:dd:ee:ff")
//	err  = wol.WakeToContext(ctx, "aa:bb:cc:dd:ee:ff", "192.168.1.255:9")
//
// A magic packet is a broadcast frame with 6 bytes of 0xFF followed by
// 16 repetitions of the target MAC address (102 bytes total).
// Most consumer NICs listen on UDP port 9 (discard) or 7 (echo).
package wol

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	gerr "github.com/golusoris/golusoris/core/errors"
)

const (
	// DefaultBroadcast is the IPv4 limited broadcast with the standard WoL port.
	DefaultBroadcast = "255.255.255.255:9"
	// DefaultTimeout bounds dialing and writing for every Wake operation.
	DefaultTimeout = 5 * time.Second

	magicLen = 6 + 6*16 // 102 bytes
)

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

// Wake sends a magic packet to the default broadcast address with
// [DefaultTimeout]. Use [WakeContext] to supply an earlier deadline or
// cancellation signal.
func Wake(mac string) error {
	return WakeContext(context.Background(), mac)
}

// WakeTo sends a magic packet to addr with [DefaultTimeout]. Use
// [WakeToContext] to supply an earlier deadline or cancellation signal.
func WakeTo(mac, addr string) error {
	return WakeToContext(context.Background(), mac, addr)
}

// WakeContext sends a magic packet to [DefaultBroadcast]. DefaultTimeout caps
// contexts without an earlier deadline.
func WakeContext(ctx context.Context, mac string) error {
	return WakeToContext(ctx, mac, DefaultBroadcast)
}

// WakeToContext sends a magic packet to addr. DefaultTimeout caps contexts
// without an earlier deadline; caller cancellation and earlier deadlines win.
func WakeToContext(ctx context.Context, mac, addr string) error {
	dialer := &net.Dialer{Timeout: DefaultTimeout}
	return wakeToContextWithDialer(ctx, mac, addr, dialer.DialContext)
}

func wakeToContextWithDialer(
	ctx context.Context,
	mac string,
	addr string,
	dial dialContextFunc,
) (err error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("wol: context: %w", ctxErr)
	}
	pkt, err := buildPacket(mac)
	if err != nil {
		return err
	}
	conn, err := dial(ctx, "udp", addr)
	if err != nil {
		return fmt.Errorf("wol: dial %s: %w", addr, err)
	}
	defer gerr.CloseInto(conn, &err, "wol: close udp conn")
	return writePacket(ctx, conn, pkt)
}

func writePacket(ctx context.Context, conn net.Conn, pkt []byte) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("wol: missing write deadline")
	}
	if deadlineErr := conn.SetWriteDeadline(deadline); deadlineErr != nil {
		return fmt.Errorf("wol: set write deadline: %w", deadlineErr)
	}
	cancelDeadlineResult := make(chan error, 1)
	stopCancelWrite := context.AfterFunc(ctx, func() {
		cancelDeadlineResult <- conn.SetWriteDeadline(deadline.Add(-DefaultTimeout))
	})
	defer stopCancelWrite()
	written, writeErr := conn.Write(pkt)
	if ctxErr := ctx.Err(); ctxErr != nil {
		select {
		case deadlineErr := <-cancelDeadlineResult:
			if deadlineErr != nil {
				return fmt.Errorf("wol: cancel write: %w", deadlineErr)
			}
		default:
		}
		return fmt.Errorf("wol: write: %w", ctxErr)
	}
	if writeErr != nil {
		return fmt.Errorf("wol: write: %w", writeErr)
	}
	if written != len(pkt) {
		return fmt.Errorf("wol: write: %w", io.ErrShortWrite)
	}
	return nil
}

// buildPacket constructs the 102-byte magic packet for mac.
func buildPacket(mac string) ([]byte, error) {
	hw, err := parseMACBytes(mac)
	if err != nil {
		return nil, fmt.Errorf("wol: parse MAC %q: %w", mac, err)
	}
	pkt := make([]byte, magicLen)
	// 6 × 0xFF header
	for i := range 6 {
		pkt[i] = 0xFF
	}
	// 16 × MAC
	for i := range 16 {
		copy(pkt[6+i*6:], hw)
	}
	return pkt, nil
}

// parseMACBytes returns the 6 raw bytes for a MAC address in common formats
// (colon-separated, hyphen-separated, or plain 12 hex chars).
func parseMACBytes(mac string) ([]byte, error) {
	clean := strings.ReplaceAll(strings.ReplaceAll(mac, ":", ""), "-", "")
	if len(clean) != 12 {
		return nil, fmt.Errorf("expected 12 hex chars, got %d", len(clean))
	}
	b, err := hex.DecodeString(clean)
	if err != nil {
		return nil, fmt.Errorf("wol: decode hex: %w", err)
	}
	return b, nil
}

// MagicPacket builds and returns the raw 102-byte magic packet without sending it.
// Useful for embedding in a raw socket or forwarding over TCP.
func MagicPacket(mac string) ([]byte, error) {
	return buildPacket(mac)
}
