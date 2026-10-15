// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package geo_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/golusoris/golusoris/db/geo"
	"github.com/golusoris/golusoris/internal/testimages"
	"github.com/golusoris/golusoris/testutil/pg"
)

func TestPostGISPoint_Integration(t *testing.T) {
	t.Parallel()

	pool := pg.Start(t, pg.Options{Image: testimages.PostGIS})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS postgis"); err != nil {
		t.Fatalf("create postgis extension: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		CREATE TEMP TABLE test_locations (
			id serial PRIMARY KEY,
			geom geometry(Point, 4326)
		)
	`); err != nil {
		t.Fatalf("create temp table: %v", err)
	}

	input := geo.Point{Lon: 13.405, Lat: 52.52}
	val, err := input.Value()
	if err != nil {
		t.Fatalf("Point.Value(): %v", err)
	}

	var id int
	if err := pool.QueryRow(ctx, "INSERT INTO test_locations (geom) VALUES (ST_GeomFromEWKT($1)) RETURNING id", val).Scan(&id); err != nil {
		t.Fatalf("insert point: %v", err)
	}

	var x, y float64
	if err := pool.QueryRow(ctx, "SELECT ST_X(geom), ST_Y(geom) FROM test_locations WHERE id = $1", id).Scan(&x, &y); err != nil {
		t.Fatalf("read ST_X/ST_Y: %v", err)
	}
	if math.Abs(x-input.Lon) > 1e-6 || math.Abs(y-input.Lat) > 1e-6 {
		t.Fatalf("ST_X/ST_Y = (%f, %f), want (%f, %f)", x, y, input.Lon, input.Lat)
	}

	var scannedEWKB geo.Point
	if err := pool.QueryRow(ctx, "SELECT geom FROM test_locations WHERE id = $1", id).Scan(&scannedEWKB); err != nil {
		t.Fatalf("scan geom EWKB: %v", err)
	}
	if math.Abs(scannedEWKB.Lon-input.Lon) > 1e-6 || math.Abs(scannedEWKB.Lat-input.Lat) > 1e-6 {
		t.Fatalf("scanned EWKB = %+v, want %+v", scannedEWKB, input)
	}

	var scannedHexText geo.Point
	if err := pool.QueryRow(ctx, "SELECT geom::text FROM test_locations WHERE id = $1", id).Scan(&scannedHexText); err != nil {
		t.Fatalf("scan geom hex text: %v", err)
	}
	if math.Abs(scannedHexText.Lon-input.Lon) > 1e-6 || math.Abs(scannedHexText.Lat-input.Lat) > 1e-6 {
		t.Fatalf("scanned hex text = %+v, want %+v", scannedHexText, input)
	}

	resetPoint := geo.Point{Lon: 10, Lat: 20}
	if err := pool.QueryRow(ctx, "SELECT NULL::geometry").Scan(&resetPoint); err != nil {
		t.Fatalf("scan NULL geometry: %v", err)
	}
	if resetPoint != (geo.Point{}) {
		t.Fatalf("NULL scan did not clear point: %+v", resetPoint)
	}

	var linestringPoint geo.Point
	if err := pool.QueryRow(ctx, "SELECT ST_GeomFromText('LINESTRING(0 0, 1 1)', 4326)").Scan(&linestringPoint); err == nil {
		t.Fatal("scan LINESTRING into Point succeeded, want error")
	}

	if input.String() != "(13.405000, 52.520000)" {
		t.Fatalf("Point.String() = %q, want %q", input.String(), "(13.405000, 52.520000)")
	}
	other := geo.Point{Lon: 13.405, Lat: 52.53}
	if dist := geo.Distance(input, other); dist <= 0 {
		t.Fatalf("Distance() = %f, want > 0", dist)
	}
}
