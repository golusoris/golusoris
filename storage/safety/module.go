// Package safety hardens user uploads before they reach a storage backend:
// metadata stripping (EXIF/GPS/XMP/text chunks dropped via stdlib re-encode),
// SSRF-guarded fetch-by-URL (dial-time IP validation re-run on every redirect
// hop via code.dny.dev/ssrf), and path-traversal-safe object keys.
//
// Security-critical (85% coverage gate). It is the only sanctioned fetch-by-URL
// entry point — a default http.Client elsewhere bypasses the SSRF guard.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    safety.Module, // provides safety.Stripper + safety.Fetcher
//	)
//
//	key, err := safety.CleanKey(userKey, 1024) // pure, no injection needed
//
// Config keys live under the "storage.safety" prefix.
package safety

import (
	"fmt"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options tunes upload hardening. Config keys live under "storage.safety".
type Options struct {
	// Strip configures metadata stripping.
	Strip StripOptions `koanf:"strip"`
	// Fetch configures the SSRF-guarded URL fetcher.
	Fetch FetchOptions `koanf:"fetch"`
	// Keys configures object-key validation.
	Keys KeyOptions `koanf:"keys"`
}

// StripOptions controls image metadata stripping.
type StripOptions struct {
	// AutoOrient bakes JPEG EXIF orientation into pixels before stripping so a
	// rotated phone photo displays upright after metadata is dropped.
	AutoOrient bool `koanf:"auto_orient"`
	// JPEGQuality is the re-encode quality (1-100) for JPEG output.
	JPEGQuality int `koanf:"jpeg_quality"`
	// MaxPixels guards against decode bombs: width*height above this rejects
	// before a full decode, checked via image.DecodeConfig.
	MaxPixels int `koanf:"max_pixels"`
}

// FetchOptions controls the SSRF-guarded fetcher.
type FetchOptions struct {
	// MaxBytes caps the response body size.
	MaxBytes int64 `koanf:"max_bytes"`
	// Timeout bounds the whole fetch (dial + redirects + body read).
	Timeout time.Duration `koanf:"timeout"`
	// AllowedSchemes is the URL scheme allowlist (default ["https"]).
	AllowedSchemes []string `koanf:"allowed_schemes"`
	// AllowHosts is an optional extra host allowlist; the SSRF guard still applies.
	AllowHosts []string `koanf:"allow_hosts"`
	// AllowPrivate disables the SSRF guard for trusted internal fetches. Logs a
	// warning when true.
	AllowPrivate bool `koanf:"allow_private"`
	// MaxRedirects bounds redirect hops; each hop is re-validated at dial time.
	MaxRedirects int `koanf:"max_redirects"`
}

// KeyOptions controls object-key validation.
type KeyOptions struct {
	// MaxLen rejects pathologically long keys.
	MaxLen int `koanf:"max_len"`
}

func defaultOptions() Options {
	return Options{
		Strip: StripOptions{
			AutoOrient:  true,
			JPEGQuality: 85,
			MaxPixels:   40_000_000,
		},
		Fetch: FetchOptions{
			MaxBytes:       33_554_432, // 32 MiB
			Timeout:        15 * time.Second,
			AllowedSchemes: []string{"https"},
			AllowHosts:     []string{},
			AllowPrivate:   false,
			MaxRedirects:   3,
		},
		Keys: KeyOptions{MaxLen: 1024},
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("storage.safety", &opts); err != nil {
		return Options{}, fmt.Errorf("storage/safety: load options: %w", err)
	}
	return opts, nil
}

// Module provides safety.Stripper and safety.Fetcher to the fx graph. CleanKey
// and MustBeLocal are pure package functions usable without injection.
var Module = fx.Module(
	"golusoris.storage.safety",
	fx.Provide(loadOptions),
	fx.Provide(newStripper),
	fx.Provide(newFetcher),
)
