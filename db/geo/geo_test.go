// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package geo_test

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/db/geo"
)

func TestDistance(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	p := geo.Point{Lon: 13.405, Lat: 52.52}
	if d := geo.Distance(p, p); d != 0 {
		t.Fatalf("expected 0 for same point, got %f", d)
	}
}

func TestDistanceNearAntipodesIsFinite(t *testing.T) {
	t.Parallel()

	a := geo.Point{Lon: -31.11084432852735, Lat: -68.42690839748464}
	b := geo.Point{Lon: 148.8891556755888, Lat: 68.4269083930225}
	distance := geo.Distance(a, b)
	if math.IsNaN(distance) || math.IsInf(distance, 0) {
		t.Fatalf("Distance() = %v, want finite near-antipodal distance", distance)
	}
}

func TestPoint_scan_nil(t *testing.T) {
	t.Parallel()
	p := geo.Point{Lon: 10, Lat: 20}
	if err := p.Scan(nil); err != nil {
		t.Fatalf("unexpected error scanning nil: %v", err)
	}
	if p != (geo.Point{}) {
		t.Fatalf("Point after NULL = %+v, want zero value", p)
	}
}

func TestPoint_scan_ewkb(t *testing.T) {
	t.Parallel()
	srid := uint32(4326)
	little := encodePoint(binary.LittleEndian, 0x20000001, &srid, 13.405, 52.52)
	big := encodePoint(binary.BigEndian, 0x20000001, &srid, -74.006, 40.7128)
	plain := encodePoint(binary.LittleEndian, 1, nil, 2.5, 1.5)
	for _, test := range []struct {
		name string
		src  any
		want geo.Point
	}{
		{name: "hex text", src: hex.EncodeToString(little), want: geo.Point{Lon: 13.405, Lat: 52.52}},
		{name: "bytea text", src: []byte(`\x` + hex.EncodeToString(little)), want: geo.Point{Lon: 13.405, Lat: 52.52}},
		{name: "raw little endian", src: little, want: geo.Point{Lon: 13.405, Lat: 52.52}},
		{name: "raw big endian", src: big, want: geo.Point{Lon: -74.006, Lat: 40.7128}},
		{name: "plain WKB", src: plain, want: geo.Point{Lon: 2.5, Lat: 1.5}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var p geo.Point
			if err := p.Scan(test.src); err != nil {
				t.Fatalf("Scan(): %v", err)
			}
			if p != test.want {
				t.Fatalf("Point = %+v, want %+v", p, test.want)
			}
		})
	}
}

func TestPoint_scan_rejects_invalid_EWKB_without_mutation(t *testing.T) {
	t.Parallel()
	srid := uint32(4326)
	wrongSRID := uint32(3857)
	valid := encodePoint(binary.LittleEndian, 0x20000001, &srid, 13.405, 52.52)
	tests := []struct {
		name string
		src  any
	}{
		{name: "wrong geometry", src: encodePoint(binary.LittleEndian, 0x20000002, &srid, 1, 2)},
		{name: "wrong SRID", src: encodePoint(binary.LittleEndian, 0x20000001, &wrongSRID, 1, 2)},
		{name: "Z dimension", src: encodePoint(binary.LittleEndian, 0xa0000001, &srid, 1, 2)},
		{name: "invalid byte order", src: append([]byte{2}, valid[1:]...)},
		{name: "short", src: valid[:20]},
		{name: "trailing bytes", src: append(append([]byte(nil), valid...), 0)},
		{name: "longitude", src: encodePoint(binary.LittleEndian, 0x20000001, &srid, 180.1, 2)},
		{name: "latitude", src: encodePoint(binary.LittleEndian, 0x20000001, &srid, 1, -90.1)},
		{name: "NaN", src: encodePoint(binary.LittleEndian, 0x20000001, &srid, math.NaN(), 2)},
		{name: "infinity", src: encodePoint(binary.LittleEndian, 0x20000001, &srid, 1, math.Inf(1))},
		{name: "bad hex", src: "xyz"},
		{name: "oversized hex string", src: strings.Repeat("00", 1024)},
		{name: "oversized hex bytes", src: []byte(strings.Repeat("00", 1024))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			p := geo.Point{Lon: 10, Lat: 20}
			if err := p.Scan(test.src); err == nil {
				t.Fatal("Scan() accepted invalid input")
			}
			if p != (geo.Point{Lon: 10, Lat: 20}) {
				t.Fatalf("Point mutated on error: %+v", p)
			}
		})
	}
}

func TestPoint_value(t *testing.T) {
	t.Parallel()
	p := geo.Point{Lon: 13.405123456789, Lat: 52.520987654321}
	v, err := p.Value()
	if err != nil {
		t.Fatal(err)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("expected string, got %T", v)
	}
	const want = "SRID=4326;POINT(13.405123456789 52.520987654321)"
	if s != want {
		t.Fatalf("Value() = %q, want %q", s, want)
	}
}

func TestPoint_value_rejects_invalid_coordinates(t *testing.T) {
	t.Parallel()
	for _, point := range []*geo.Point{
		nil,
		{Lon: math.NaN()},
		{Lon: math.Inf(1)},
		{Lon: 181},
		{Lat: 91},
	} {
		if _, err := point.Value(); err == nil {
			t.Fatalf("Value() accepted %+v", point)
		}
	}
	for _, point := range []geo.Point{
		{Lon: -180, Lat: -90},
		{Lon: 180, Lat: 90},
	} {
		value, err := point.Value()
		if err != nil {
			t.Fatalf("Value(%+v): %v", point, err)
		}
		if !strings.HasPrefix(value.(string), "SRID=4326;POINT(") {
			t.Fatalf("Value(%+v) = %q", point, value)
		}
	}
}

func encodePoint(
	order binary.ByteOrder,
	typeCode uint32,
	srid *uint32,
	lon float64,
	lat float64,
) []byte {
	size := 21
	offset := 5
	if srid != nil {
		size = 25
		offset = 9
	}
	b := make([]byte, size)
	if order == binary.LittleEndian {
		b[0] = 1
	}
	order.PutUint32(b[1:5], typeCode)
	if srid != nil {
		order.PutUint32(b[5:9], *srid)
	}
	order.PutUint64(b[offset:offset+8], math.Float64bits(lon))
	order.PutUint64(b[offset+8:offset+16], math.Float64bits(lat))
	return b
}
