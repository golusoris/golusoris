package pipeline

import "time"

// Default bounds. These cap untrusted resize requests to defeat
// decompression-bomb / DoS resizes (Power-of-10 rule 2: every loop and every
// allocation is bounded).
const (
	// DefaultMaxWidth caps requested output width when Options.MaxWidth is 0.
	DefaultMaxWidth = 4096
	// DefaultMaxHeight caps requested output height when Options.MaxHeight is 0.
	DefaultMaxHeight = 4096
	// DefaultMaxPixels caps width*height when Options.MaxPixels is 0 (16 MP).
	DefaultMaxPixels = 16 << 20
	// DefaultCacheControl is the Cache-Control header for served variants.
	DefaultCacheControl = "public, max-age=31536000, immutable"
	// DefaultDefaultTTL is the signed-URL lifetime used by Sign(0).
	DefaultDefaultTTL = 5 * time.Minute
)

// DefaultAllowedFormats is the format allowlist used when Options.AllowedFormats
// is empty. AVIF/TIFF are opt-in (heavier encode) and excluded by default.
var DefaultAllowedFormats = []string{"jpeg", "png", "webp", "gif"}

// Options tunes signing and the resize bounds. The signing secret has no
// default — an empty secret is rejected at construction so an app cannot
// accidentally ship an unauthenticated resize proxy.
//
// Config keys live under the "media.img.pipeline" prefix.
type Options struct {
	// Secret is the HMAC signing key (>=16 bytes). REQUIRED; no default.
	Secret string `koanf:"secret"`
	// AllowedFormats is the output-format allowlist. Empty => DefaultAllowedFormats.
	AllowedFormats []string `koanf:"allowed_formats"`
	// MaxWidth caps requested output width. 0 => DefaultMaxWidth.
	MaxWidth int `koanf:"max_width"`
	// MaxHeight caps requested output height. 0 => DefaultMaxHeight.
	MaxHeight int `koanf:"max_height"`
	// MaxPixels caps width*height. 0 => DefaultMaxPixels.
	MaxPixels int `koanf:"max_pixels"`
	// CacheControl is the Cache-Control header on served variants.
	// Empty => DefaultCacheControl.
	CacheControl string `koanf:"cache_control"`
	// DefaultTTL is the signed-URL lifetime when Sign is called with ttl<=0.
	// 0 => DefaultDefaultTTL.
	DefaultTTL time.Duration `koanf:"default_ttl"`
}

// withDefaults returns a copy with zero fields replaced by package defaults.
// Secret is intentionally left untouched: it is validated by [New].
func (o Options) withDefaults() Options {
	if len(o.AllowedFormats) == 0 {
		o.AllowedFormats = DefaultAllowedFormats
	}
	if o.MaxWidth <= 0 {
		o.MaxWidth = DefaultMaxWidth
	}
	if o.MaxHeight <= 0 {
		o.MaxHeight = DefaultMaxHeight
	}
	if o.MaxPixels <= 0 {
		o.MaxPixels = DefaultMaxPixels
	}
	if o.CacheControl == "" {
		o.CacheControl = DefaultCacheControl
	}
	if o.DefaultTTL <= 0 {
		o.DefaultTTL = DefaultDefaultTTL
	}
	return o
}
