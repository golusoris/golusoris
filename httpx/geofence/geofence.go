// Package geofence provides country-level allow/deny middleware backed by a
// MaxMind mmdb file (e.g. GeoLite2-Country).
//
// The mmdb file is app-supplied — the framework doesn't bundle it
// (MaxMind's license requires attribution + per-deployer license keys).
// Apps pass the file path (or the opened Reader) via Options.
//
// Config keys (env: APP_HTTP_GEOFENCE_*):
//
//	http.geofence.mmdb   # path to GeoLite2-Country.mmdb
//	http.geofence.allow  # ISO-3166-1 alpha-2 codes, comma-separated (empty = allow all)
//	http.geofence.deny   # ISO-3166-1 alpha-2 codes, comma-separated (empty = deny none)
//
// If allow is non-empty, only listed countries pass. If allow is empty and
// deny is non-empty, listed countries are blocked. Both empty → pass-through.
package geofence

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/oschwald/maxminddb-golang"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/middleware"
)

// Options configures the geofence middleware.
type Options struct {
	MmdbPath string   `koanf:"mmdb"`
	Allow    []string `koanf:"allow"`
	Deny     []string `koanf:"deny"`
}

// Reader is the minimum of maxminddb.Reader used by the middleware. Useful
// for tests + for apps that want to supply a pre-opened reader instead of a
// path.
type Reader interface {
	Lookup(ip net.IP, result any) error
	Close() error
}

// Record is the subset of the GeoLite2-Country schema we need.
type Record struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
}

// New builds the middleware. If opts.MmdbPath is empty AND both Allow/Deny
// are empty, returns a no-op so geofence is fully opt-in. An empty
// MmdbPath with a non-empty policy is an error.
func New(opts Options) (middleware.Middleware, Reader, error) {
	hasPolicy := len(opts.Allow) > 0 || len(opts.Deny) > 0
	if !hasPolicy && opts.MmdbPath == "" {
		return identity, nil, nil
	}
	if opts.MmdbPath == "" {
		return nil, nil, fmt.Errorf("httpx/geofence: MmdbPath required when Allow/Deny set")
	}
	r, err := maxminddb.Open(opts.MmdbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("httpx/geofence: open %s: %w", opts.MmdbPath, err)
	}
	return newFromReader(opts, r), r, nil
}

// newFromReader builds the middleware closure. Exported-lite via NewFromReader
// so tests can inject a fake.
func newFromReader(opts Options, r Reader) middleware.Middleware {
	allow := toSet(opts.Allow)
	deny := toSet(opts.Deny)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !permitted(r, req, allow, deny) {
				http.Error(w, "forbidden by geofence", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// NewFromReader builds the middleware from an already-open Reader.
// Apps in tests or with a custom Reader source use this instead of [New].
func NewFromReader(opts Options, r Reader) middleware.Middleware {
	return newFromReader(opts, r)
}

func permitted(r Reader, req *http.Request, allow, deny map[string]struct{}) bool {
	ip := clientIP(req)
	if ip == nil {
		// If we can't identify the peer, fail open only when no policy is set.
		return len(allow) == 0 && len(deny) == 0
	}
	var rec Record
	if err := r.Lookup(ip, &rec); err != nil {
		return false
	}
	code := strings.ToUpper(rec.Country.ISOCode)
	if len(allow) > 0 {
		_, ok := allow[code]
		return ok
	}
	_, blocked := deny[code]
	return !blocked
}

func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}

func toSet(codes []string) map[string]struct{} {
	out := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		if c = strings.ToUpper(strings.TrimSpace(c)); c != "" {
			out[c] = struct{}{}
		}
	}
	return out
}

func identity(next http.Handler) http.Handler { return next }

func loadOptions(cfg *config.Config) (Options, error) {
	opts := Options{}
	if err := cfg.Unmarshal("http.geofence", &opts); err != nil {
		return Options{}, fmt.Errorf("httpx/geofence: load options: %w", err)
	}
	return opts, nil
}

// Module provides a geofence [middleware.Middleware] + the open [Reader] so
// apps can close it on shutdown. Opens the mmdb during fx provide; if the
// file is missing + no policy is set, the module is a no-op.
var Module = fx.Module("golusoris.httpx.geofence",
	fx.Provide(
		loadOptions,
		func(lc fx.Lifecycle, opts Options) (middleware.Middleware, error) {
			mw, reader, err := New(opts)
			if err != nil {
				return nil, err
			}
			if reader != nil {
				lc.Append(fx.Hook{
					OnStop: func(_ context.Context) error {
						_ = reader.Close()
						return nil
					},
				})
			}
			return mw, nil
		},
	),
)
