// Package wol sends Wake-on-LAN magic packets over UDP.
//
// Usage:
//
//	err := wol.Wake("aa:bb:cc:dd:ee:ff")
//	err  = wol.WakeTo("aa:bb:cc:dd:ee:ff", "192.168.1.255:9")
//
// A magic packet is a broadcast frame with 6 bytes of 0xFF followed by
// 16 repetitions of the target MAC address (102 bytes total).
// Most consumer NICs listen on UDP port 9 (discard) or 7 (echo).
package wol

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
)

const (
	// DefaultBroadcast is the IPv4 limited broadcast with the standard WoL port.
	DefaultBroadcast = "255.255.255.255:9"

	magicLen = 6 + 6*16 // 102 bytes
)

// Wake sends a magic packet to the default broadcast address.
func Wake(mac string) error {
	return WakeTo(mac, DefaultBroadcast)
}

// WakeTo sends a magic packet to the given UDP addr (e.g. "192.168.1.255:9").
func WakeTo(mac, addr string) error {
	pkt, err := buildPacket(mac)
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "udp", addr)
	if err != nil {
		return fmt.Errorf("wol: dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write(pkt); err != nil {
		return fmt.Errorf("wol: write: %w", err)
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
