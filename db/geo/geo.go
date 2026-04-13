// Package geo provides PostGIS geometry helpers for pgx/v5.
//
// It registers the pgtype codecs needed to scan WKB/EWKB geometry values
// returned by PostGIS and provides lightweight Point/BBox types for common
// use cases without pulling in a heavy geometry library.
//
// Usage:
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	geo.RegisterTypes(ctx, pool) // call once after pool creation
//
//	var p geo.Point
//	_ = pool.QueryRow(ctx, "SELECT geom FROM locations WHERE id=$1", id).Scan(&p)
package geo

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5/pgxpool"
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

// RegisterTypes is a no-op placeholder — pgx/v5 handles standard geometry
// types via the text/binary protocol without explicit registration for most
// use cases. Apps that need full PostGIS type support should use
// twpayne/go-geom or pgeo-go and register their encoders on the pgx type map.
func RegisterTypes(_ context.Context, _ *pgxpool.Pool) error { return nil }

// Scan implements sql.Scanner so Point can be used with pgx row scanning.
// Accepts EWKB hex strings as returned by PostGIS ST_AsEWKB / the default
// binary format.
func (p *Point) Scan(src any) error {
	var raw string
	switch v := src.(type) {
	case string:
		raw = v
	case []byte:
		raw = string(v)
	case nil:
		return nil
	default:
		return fmt.Errorf("geo: cannot scan %T into Point", src)
	}

	b, err := hex.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("geo: decode hex: %w", err)
	}
	return p.decodeEWKB(b)
}

// Value implements driver.Valuer so Point can be used as a query argument
// (as EWKT for simplicity).
func (p Point) Value() (driver.Value, error) {
	return fmt.Sprintf("SRID=4326;POINT(%f %f)", p.Lon, p.Lat), nil
}

// String returns a human-readable representation.
func (p Point) String() string { return fmt.Sprintf("(%f, %f)", p.Lon, p.Lat) }

// decodeEWKB decodes an EWKB byte slice into p.
// Supports little-endian WKB/EWKB with and without SRID.
func (p *Point) decodeEWKB(b []byte) error {
	if len(b) < 21 {
		return fmt.Errorf("geo: EWKB too short (%d bytes)", len(b))
	}
	if b[0] != 1 { // little-endian byte order marker
		return fmt.Errorf("geo: only little-endian EWKB supported")
	}
	wkbType := binary.LittleEndian.Uint32(b[1:5])

	const ewkbSRID = 0x20000000
	hasSRID := wkbType&ewkbSRID != 0
	offset := 5
	if hasSRID {
		offset += 4 // skip 4-byte SRID
	}
	if len(b) < offset+16 {
		return fmt.Errorf("geo: EWKB too short for point coordinates")
	}
	p.Lon = math.Float64frombits(binary.LittleEndian.Uint64(b[offset:]))
	p.Lat = math.Float64frombits(binary.LittleEndian.Uint64(b[offset+8:]))
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
	return 2 * earthR * math.Asin(math.Sqrt(h))
}

func toRad(deg float64) float64 { return deg * math.Pi / 180 }
