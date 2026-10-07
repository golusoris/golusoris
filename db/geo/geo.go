// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package geo provides a lightweight PostGIS Point scanner, value encoder,
// and Haversine distance helper for pgx/v5.
//
// Usage:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//
//	var p geo.Point
//	_ = pool.QueryRow(ctx, "SELECT ST_AsEWKB(geom) FROM locations WHERE id=$1", id).Scan(&p)
package geo

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ewkbTypePoint = uint32(1)
	ewkbZ         = uint32(0x80000000)
	ewkbM         = uint32(0x40000000)
	ewkbSRID      = uint32(0x20000000)
	wgs84SRID     = uint32(4326)
	wkbPointSize  = 21
	ewkbPointSize = 25
)

var (
	errNilPoint         = errors.New("nil Point")
	errUnsupportedPoint = errors.New("only 2D points are supported")
)

// Point is a 2D geographic coordinate (longitude, latitude).
type Point struct {
	Lon float64
	Lat float64
}

// BBox is a 2D bounding box.
type BBox struct {
	MinLon, MinLat, MaxLon, MaxLat float64
}

// RegisterTypes is retained for compatibility and performs no registration.
//
// Deprecated: Point implements sql.Scanner and driver.Valuer directly. Use a
// geometry library for additional PostGIS types and codecs.
func RegisterTypes(_ context.Context, _ *pgxpool.Pool) error { return nil }

// Scan implements sql.Scanner so Point can be used with pgx row scanning.
// It accepts raw WKB/EWKB bytes and hexadecimal text, including PostgreSQL's
// \x-prefixed bytea rendering. SQL NULL clears the point.
func (p *Point) Scan(src any) error {
	if p == nil {
		return fmt.Errorf("geo: scan: %w", errNilPoint)
	}
	raw, isNull, err := scanEWKBSource(src)
	if err != nil {
		return err
	}
	if isNull {
		*p = Point{}
		return nil
	}
	decoded, err := decodeEWKB(raw)
	if err != nil {
		return err
	}
	*p = decoded
	return nil
}

func scanEWKBSource(src any) ([]byte, bool, error) {
	switch v := src.(type) {
	case string:
		decoded, err := decodeHexEWKB(v)
		if err != nil {
			return nil, false, err
		}
		return decoded, false, nil
	case []byte:
		if len(v) > 0 && (v[0] == 0 || v[0] == 1) {
			return v, false, nil
		}
		if !validEncodedPointSize(len(v)) {
			return nil, false, fmt.Errorf("geo: Point EWKB text length %d is invalid", len(v))
		}
		decoded, err := decodeHexEWKB(string(v))
		if err != nil {
			return nil, false, err
		}
		return decoded, false, nil
	case nil:
		return nil, true, nil
	default:
		return nil, false, fmt.Errorf("geo: cannot scan %T into Point", src)
	}
}

func decodeHexEWKB(value string) ([]byte, error) {
	value = strings.TrimPrefix(value, `\x`)
	if len(value) != 2*wkbPointSize && len(value) != 2*ewkbPointSize {
		return nil, fmt.Errorf("geo: Point EWKB hex length %d is invalid", len(value))
	}
	b, err := hex.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("geo: decode hex: %w", err)
	}
	return b, nil
}

func validEncodedPointSize(size int) bool {
	return size == 2*wkbPointSize ||
		size == 2*ewkbPointSize ||
		size == 2+2*wkbPointSize ||
		size == 2+2*ewkbPointSize
}

// Value implements driver.Valuer so Point can be used as a query argument
// (as EWKT for simplicity).
func (p *Point) Value() (driver.Value, error) {
	if p == nil {
		return nil, fmt.Errorf("geo: value: %w", errNilPoint)
	}
	if err := validateCoordinates(p.Lon, p.Lat); err != nil {
		return nil, err
	}
	lon := strconv.FormatFloat(p.Lon, 'g', -1, 64)
	lat := strconv.FormatFloat(p.Lat, 'g', -1, 64)
	return "SRID=4326;POINT(" + lon + " " + lat + ")", nil
}

// String returns a human-readable representation.
func (p *Point) String() string { return fmt.Sprintf("(%f, %f)", p.Lon, p.Lat) }

// decodeEWKB decodes one 2D Point in WKB or PostGIS EWKB form.
func decodeEWKB(b []byte) (Point, error) {
	order, offset, wantSize, err := pointLayout(b)
	if err != nil {
		return Point{}, err
	}
	if len(b) != wantSize {
		return Point{}, fmt.Errorf("geo: Point EWKB length %d, want %d", len(b), wantSize)
	}
	point := Point{
		Lon: math.Float64frombits(order.Uint64(b[offset : offset+8])),
		Lat: math.Float64frombits(order.Uint64(b[offset+8 : offset+16])),
	}
	if err := validateCoordinates(point.Lon, point.Lat); err != nil {
		return Point{}, err
	}
	return point, nil
}

func pointLayout(b []byte) (binary.ByteOrder, int, int, error) {
	if len(b) < 5 {
		return nil, 0, 0, fmt.Errorf("geo: EWKB too short (%d bytes)", len(b))
	}
	order, err := ewkbByteOrder(b[0])
	if err != nil {
		return nil, 0, 0, err
	}
	wkbType := order.Uint32(b[1:5])
	if wkbType&(ewkbZ|ewkbM) != 0 {
		return nil, 0, 0, fmt.Errorf("geo: decode Point: %w", errUnsupportedPoint)
	}
	if wkbType&^ewkbSRID != ewkbTypePoint {
		return nil, 0, 0, fmt.Errorf("geo: geometry type %d is not Point", wkbType&^ewkbSRID)
	}
	if wkbType&ewkbSRID == 0 {
		return order, 5, wkbPointSize, nil
	}
	if len(b) < 9 {
		return nil, 0, 0, fmt.Errorf("geo: EWKB too short (%d bytes)", len(b))
	}
	if srid := order.Uint32(b[5:9]); srid != wgs84SRID {
		return nil, 0, 0, fmt.Errorf("geo: unsupported SRID %d, want %d", srid, wgs84SRID)
	}
	return order, 9, ewkbPointSize, nil
}

func ewkbByteOrder(marker byte) (binary.ByteOrder, error) {
	switch marker {
	case 0:
		return binary.BigEndian, nil
	case 1:
		return binary.LittleEndian, nil
	default:
		return nil, fmt.Errorf("geo: invalid EWKB byte order %d", marker)
	}
}

func validateCoordinates(lon float64, lat float64) error {
	if math.IsNaN(lon) || math.IsInf(lon, 0) || lon < -180 || lon > 180 {
		return fmt.Errorf("geo: longitude %v outside [-180, 180]", lon)
	}
	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
		return fmt.Errorf("geo: latitude %v outside [-90, 90]", lat)
	}
	return nil
}

// Distance returns the approximate great-circle distance in metres between two
// points using the Haversine formula.
func Distance(a, b Point) float64 {
	const earthR = 6_371_000.0
	dLat := toRad(b.Lat - a.Lat)
	dLon := toRad(b.Lon - a.Lon)
	sinLat := math.Sin(dLat / 2)
	sinLon := math.Sin(dLon / 2)
	h := sinLat*sinLat + math.Cos(toRad(a.Lat))*math.Cos(toRad(b.Lat))*sinLon*sinLon
	h = min(h, 1)
	return 2 * earthR * math.Asin(math.Sqrt(h))
}

func toRad(deg float64) float64 { return deg * math.Pi / 180 }
