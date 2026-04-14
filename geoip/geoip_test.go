package geoip_test

import (
	"net"
	"testing"

	"github.com/golusoris/golusoris/geoip"
)

// These tests exercise the API surface without a real mmdb file.
// A real integration test would require a GeoLite2-City.mmdb fixture.

func TestOpen_badPath(t *testing.T) {
	t.Parallel()
	_, err := geoip.Open("/nonexistent/does-not-exist.mmdb")
	if err == nil {
		t.Fatal("expected error opening nonexistent mmdb")
	}
}

func TestCountryCode_noOpen(t *testing.T) {
	t.Parallel()
	// Verify that a bad open returns an error (not a panic).
	db, err := geoip.Open("/nonexistent.mmdb")
	if err == nil {
		t.Fatal("expected error")
	}
	if db != nil {
		t.Fatal("expected nil DB on error")
	}
}

func TestParseIP(t *testing.T) {
	t.Parallel()
	// Ensure the standard net.ParseIP is usable as input.
	ip := net.ParseIP("8.8.8.8")
	if ip == nil {
		t.Fatal("net.ParseIP returned nil")
	}
}
