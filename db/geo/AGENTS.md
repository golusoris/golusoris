<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — db/geo/

Small PostGIS Point helper. No geometry dependency.

## Usage

```go
pool, _ := pgxpool.New(ctx, dsn) // TimescaleDB/PostGIS-enabled Postgres
// Scan a geometry column:
var p geo.Point
_ = pool.QueryRow(ctx, "SELECT ST_AsEWKB(geom) FROM locations WHERE id=$1", id).Scan(&p)

// Use as query argument (inserts as EWKT):
_, _ = pool.Exec(ctx, "INSERT INTO locations (geom) VALUES (ST_GeomFromEWKT($1))", p)

// Great-circle distance (Haversine):
nyc := geo.Point{Lon: -74.006, Lat: 40.7128}
lax := geo.Point{Lon: -118.2437, Lat: 34.0522}
metres := geo.Distance(nyc, lax) // ≈ 3_940_000
```

## Contract

- `Point.Scan`: raw or hex 2D WKB/EWKB, either byte order. SRID absent or 4326.
- `Point`: finite WGS84 longitude/latitude only. SQL NULL clears prior value.
- `Point.Value`: EWKT with SRID 4326.
- `Distance`: approximate Haversine metres; accumulator clamped for antipodal stability.
- `BBox`: data type only. No scanner or valuer.
- `RegisterTypes`: deprecated compatibility no-op.

## Don't

- Don't pass `ST_AsText` to `Scan`. It expects hex EWKB.
- Don't use `Distance` for precise routing. Use PostGIS geography distance.
- Don't claim general PostGIS codec support. Bring a geometry library.
