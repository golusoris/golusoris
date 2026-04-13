// Package geoip provides MaxMind GeoLite2 / GeoIP2 database lookups.
// The caller supplies the mmdb file path; the framework does not bundle the
// database (it is licensed separately by MaxMind).
//
// Usage:
//
//	db, err := geoip.Open("/var/geoip/GeoLite2-City.mmdb")
//	defer db.Close()
//
//	info, err := db.Lookup(net.ParseIP("8.8.8.8"))
//	// info.Country.ISOCode == "US"
package geoip

import (
	"fmt"
	"net"

	"github.com/oschwald/maxminddb-golang"
)

// Info contains the fields populated from a GeoLite2-City lookup.
// Fields may be zero-valued when the database does not contain information
// for the queried IP.
type Info struct {
	Country  Country
	City     City
	Location Location
	ASN      ASN
}

// Country holds country-level data.
type Country struct {
	ISOCode string            // ISO 3166-1 alpha-2 (e.g. "US")
	Names   map[string]string // locale → name (e.g. "en" → "United States")
}

// City holds city-level data.
type City struct {
	Names map[string]string // locale → name
}

// Location holds geographic coordinates and timezone.
type Location struct {
	Latitude  float64
	Longitude float64
	TimeZone  string
}

// ASN holds Autonomous System information (requires GeoLite2-ASN.mmdb).
type ASN struct {
	Number       uint   // AS number
	Organization string // e.g. "Google LLC"
}

// mmdbCityRecord mirrors the GeoLite2-City record structure.
type mmdbCityRecord struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Location struct {
		Latitude  float64 `maxminddb:"latitude"`
		Longitude float64 `maxminddb:"longitude"`
		TimeZone  string  `maxminddb:"time_zone"`
	} `maxminddb:"location"`
}

// mmdbASNRecord mirrors the GeoLite2-ASN record structure.
type mmdbASNRecord struct {
	ASNumber           uint   `maxminddb:"autonomous_system_number"`
	ASOrganization     string `maxminddb:"autonomous_system_organization"`
}

// DB wraps an open MaxMind database.
type DB struct {
	db *maxminddb.Reader
}

// Open opens the mmdb file at path. Call [DB.Close] when done.
func Open(path string) (*DB, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("geoip: open %s: %w", path, err)
	}
	return &DB{db: r}, nil
}

// Close releases the database file handle.
func (d *DB) Close() error { return d.db.Close() }

// Lookup returns geographic information for ip. Returns a zero Info and nil
// error when the IP is not found in the database (e.g. RFC 1918 addresses).
func (d *DB) Lookup(ip net.IP) (Info, error) {
	var rec mmdbCityRecord
	if err := d.db.Lookup(ip, &rec); err != nil {
		return Info{}, fmt.Errorf("geoip: lookup %s: %w", ip, err)
	}
	return Info{
		Country: Country{
			ISOCode: rec.Country.ISOCode,
			Names:   rec.Country.Names,
		},
		City: City{Names: rec.City.Names},
		Location: Location{
			Latitude:  rec.Location.Latitude,
			Longitude: rec.Location.Longitude,
			TimeZone:  rec.Location.TimeZone,
		},
	}, nil
}

// LookupASN returns ASN information for ip. Requires a GeoLite2-ASN database.
func (d *DB) LookupASN(ip net.IP) (ASN, error) {
	var rec mmdbASNRecord
	if err := d.db.Lookup(ip, &rec); err != nil {
		return ASN{}, fmt.Errorf("geoip: asn lookup %s: %w", ip, err)
	}
	return ASN{Number: rec.ASNumber, Organization: rec.ASOrganization}, nil
}

// CountryCode is a convenience wrapper returning just the ISO country code.
// Returns "" when not found.
func (d *DB) CountryCode(ip net.IP) string {
	info, err := d.Lookup(ip)
	if err != nil {
		return ""
	}
	return info.Country.ISOCode
}
