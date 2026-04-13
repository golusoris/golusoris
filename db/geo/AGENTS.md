# Agent guide — db/geo/

PostGIS geometry helpers for pgx/v5.

No heavy geometry dependency — provides `Point` (2D lon/lat) and `BBox`
types with `sql.Scanner` / `driver.Valuer` so they work directly with
pgx row scanning and query arguments.

## Usage

```go
pool, _ := pgxpool.New(ctx, dsn) // TimescaleDB/PostGIS-enabled Postgres
geo.RegisterTypes(ctx, pool)     // currently a no-op; wired for future type registration

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

## EWKB scanning

`Point.Scan` accepts hex-encoded EWKB strings as returned by `ST_AsEWKB()`.
It supports little-endian WKB/EWKB with and without SRID.

## Don't

- Don't pass `ST_AsText` output to `Scan` — it expects hex EWKB.
- Don't use `Distance` for precision routing — it's Haversine (spherical earth).
  Use PostGIS `ST_Distance(geography, geography)` for accurate geodesic distance.
