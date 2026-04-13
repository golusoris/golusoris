package geo_test

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"

	"github.com/golusoris/golusoris/db/geo"
)

func TestDistance(t *testing.T) {
	// New York → Los Angeles ≈ 3,940 km
	nyc := geo.Point{Lon: -74.006, Lat: 40.7128}
	lax := geo.Point{Lon: -118.2437, Lat: 34.0522}
	d := geo.Distance(nyc, lax)
	const want = 3_940_000.0
	const tolerancePct = 0.02 // 2%
	if math.Abs(d-want)/want > tolerancePct {
		t.Fatalf("Distance NYC→LAX: got %.0f m, want ~%.0f m (±%.0f%%)", d, want, tolerancePct*100)
	}
}

func TestDistance_same(t *testing.T) {
	p := geo.Point{Lon: 13.405, Lat: 52.52}
	if d := geo.Distance(p, p); d != 0 {
		t.Fatalf("expected 0 for same point, got %f", d)
	}
}

func TestPoint_scan_nil(t *testing.T) {
	var p geo.Point
	if err := p.Scan(nil); err != nil {
		t.Fatalf("unexpected error scanning nil: %v", err)
	}
}

func TestPoint_scan_ewkb(t *testing.T) {
	// Build a little-endian EWKB for POINT(13.405 52.52) with SRID 4326.
	buf := make([]byte, 25)
	buf[0] = 1 // little-endian
	binary.LittleEndian.PutUint32(buf[1:], 0x20000001) // Point | SRID flag
	binary.LittleEndian.PutUint32(buf[5:], 4326)       // SRID
	binary.LittleEndian.PutUint64(buf[9:], math.Float64bits(13.405))
	binary.LittleEndian.PutUint64(buf[17:], math.Float64bits(52.52))

	var p geo.Point
	if err := p.Scan(hex.EncodeToString(buf)); err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if math.Abs(p.Lon-13.405) > 1e-9 || math.Abs(p.Lat-52.52) > 1e-9 {
		t.Fatalf("got (%f, %f), want (13.405, 52.52)", p.Lon, p.Lat)
	}
}

func TestPoint_value(t *testing.T) {
	p := geo.Point{Lon: 13.405, Lat: 52.52}
	v, err := p.Value()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("expected string, got %T", v)
	}
	if s == "" {
		t.Fatal("empty value")
	}
}
