package audio

import "time"

// Defaults for unset Options fields.
const (
	// DefaultMaxDecodedBytes caps PCM materialized per call (anti-DoS): 512 MiB.
	DefaultMaxDecodedBytes int64 = 512 << 20
	// DefaultPeakBuckets is the waveform resolution when the caller passes 0.
	DefaultPeakBuckets = 2048
)

// Options tunes the analyzer. The zero value is usable: it decodes any
// supported format, caps decode at DefaultMaxDecodedBytes, and renders
// DefaultPeakBuckets waveform buckets.
//
// Config keys live under the "media.audio" prefix.
type Options struct {
	// Backend selects the implementation. Only "pureGo" exists today; the key
	// is reserved for a future "ffmpeg" delegation to media/av.
	Backend string `koanf:"backend"`
	// MaxDecodedBytes caps total PCM a single Waveform/Loudness call may
	// materialize (anti-DoS on untrusted input). 0 => DefaultMaxDecodedBytes.
	MaxDecodedBytes int64 `koanf:"max_decoded_bytes"`
	// MaxDuration rejects inputs whose probed length exceeds it. 0 => no cap.
	MaxDuration time.Duration `koanf:"max_duration"`
	// DefaultPeakBuckets is used by Waveform when the caller passes 0.
	DefaultPeakBuckets int `koanf:"default_peak_buckets"`
}

// withDefaults returns a copy with zero fields replaced by package defaults.
func (o Options) withDefaults() Options {
	if o.MaxDecodedBytes <= 0 {
		o.MaxDecodedBytes = DefaultMaxDecodedBytes
	}
	if o.DefaultPeakBuckets <= 0 {
		o.DefaultPeakBuckets = DefaultPeakBuckets
	}
	if o.Backend == "" {
		o.Backend = "pureGo"
	}
	return o
}
