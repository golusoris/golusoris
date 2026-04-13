# Agent guide — geoip/

Thin wrapper around `oschwald/maxminddb-golang` for MaxMind GeoLite2 / GeoIP2 database lookups.
No fx module — caller manages the `*DB` lifecycle (Open/Close).

## Usage

```go
db, err := geoip.Open("/var/geoip/GeoLite2-City.mmdb")
defer db.Close()

info, err := db.Lookup(net.ParseIP("8.8.8.8"))
// info.Country.ISOCode == "US"
// info.Location.TimeZone == "America/Chicago"

code := db.CountryCode(net.ParseIP("8.8.8.8")) // "US" or ""

asnDB, _ := geoip.Open("/var/geoip/GeoLite2-ASN.mmdb")
asn, _ := asnDB.LookupASN(net.ParseIP("8.8.8.8"))
// asn.Number == 15169, asn.Organization == "Google LLC"
```

## Notes

- `Lookup` returns a zero `Info` and nil error for RFC 1918 / unrecognised IPs.
- `LookupASN` requires a separate GeoLite2-ASN.mmdb file (different database type).
- The mmdb file is NOT bundled — obtain from MaxMind and supply the path at runtime.
- Integrate with `httpx/geofence/` to block or redirect based on `CountryCode`.

## Don't

- Don't call `Lookup` per request without caching — mmdb reads are cheap but country
  results rarely change; cache at the IP level for hot paths.
- Don't use `LookupASN` on a city database or vice-versa; the struct tags won't match.
