// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package audio

import "time"

// Defaults for unset Options fields.
const (
	// DefaultMaxInputBytes caps encoded bytes consumed per call: 512 MiB.
	DefaultMaxInputBytes int64 = 512 << 20
	// DefaultMaxDecodedBytes caps PCM materialized per call (anti-DoS): 512 MiB.
	DefaultMaxDecodedBytes int64 = 512 << 20
	// DefaultPeakBuckets is the waveform resolution when the caller passes 0.
	DefaultPeakBuckets = 2048
	// DefaultMaxPeakBuckets bounds waveform result and accumulator allocation.
	DefaultMaxPeakBuckets = 1 << 20
	// DefaultMaxChannels rejects forged container metadata before allocation.
	DefaultMaxChannels = 64
)

// Options tunes the analyzer. The zero value is usable: it decodes any
// supported format, caps encoded input at DefaultMaxInputBytes, caps decode at
// DefaultMaxDecodedBytes, and renders DefaultPeakBuckets waveform buckets.
//
// Config keys live under the "media.audio" prefix.
type Options struct {
	// Backend selects the implementation. Only "pureGo" exists today; the key
	// is reserved for a future "ffmpeg" delegation to media/av.
	Backend string `koanf:"backend"`
	// MaxInputBytes caps encoded bytes read before and during decoder use.
	// Non-positive => DefaultMaxInputBytes.
	MaxInputBytes int64 `koanf:"max_input_bytes"`
	// MaxDecodedBytes caps total PCM a single Waveform/Loudness call may
	// materialize (anti-DoS on untrusted input). Non-positive => DefaultMaxDecodedBytes.
	MaxDecodedBytes int64 `koanf:"max_decoded_bytes"`
	// MaxChannels caps container channel metadata before decoder allocation.
	// Non-positive => DefaultMaxChannels.
	MaxChannels int `koanf:"max_channels"`
	// MaxDuration rejects inputs whose probed length exceeds it. 0 => no cap.
	MaxDuration time.Duration `koanf:"max_duration"`
	// DefaultPeakBuckets is used by Waveform when the caller passes 0.
	// Non-positive => DefaultPeakBuckets.
	DefaultPeakBuckets int `koanf:"default_peak_buckets"`
	// MaxPeakBuckets bounds caller-selected waveform resolution.
	// Non-positive => DefaultMaxPeakBuckets.
	MaxPeakBuckets int `koanf:"max_peak_buckets"`
}

// withDefaults returns a copy with non-positive bounds replaced by defaults.
func (o Options) withDefaults() Options {
	if o.MaxInputBytes <= 0 {
		o.MaxInputBytes = DefaultMaxInputBytes
	}
	if o.MaxDecodedBytes <= 0 {
		o.MaxDecodedBytes = DefaultMaxDecodedBytes
	}
	if o.DefaultPeakBuckets <= 0 {
		o.DefaultPeakBuckets = DefaultPeakBuckets
	}
	if o.MaxPeakBuckets <= 0 {
		o.MaxPeakBuckets = DefaultMaxPeakBuckets
	}
	if o.MaxChannels <= 0 {
		o.MaxChannels = DefaultMaxChannels
	}
	if o.Backend == "" {
		o.Backend = "pureGo"
	}
	return o
}
