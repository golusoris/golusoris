// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package audio

import (
	"context"
	"math"
	"testing"
	"time"
)

// stalledStream never advances or terminates: read always returns 0 frames
// and a nil error. It exercises the HISS-02 chunk bound in
// streamMono/loudness that guards against a decoder stuck without EOF.
type stalledStream struct {
	ch int
}

func (s stalledStream) info() Info                  { return Info{Channels: s.ch, SampleRate: 48000} }
func (stalledStream) read(_ []float32) (int, error) { return 0, nil }

func TestDecodeChunkLimit(t *testing.T) {
	t.Parallel()
	if got := decodeChunkLimit(0); got != decodeChunkNoLimitGuard {
		t.Errorf("decodeChunkLimit(0) = %d, want %d", got, decodeChunkNoLimitGuard)
	}
	if got := decodeChunkLimit(-5); got != decodeChunkNoLimitGuard {
		t.Errorf("decodeChunkLimit(-5) = %d, want %d", got, decodeChunkNoLimitGuard)
	}
	if got := decodeChunkLimit(10); got != 11 {
		t.Errorf("decodeChunkLimit(10) = %d, want 11", got)
	}
}

// TestStreamMono_BoundedAgainstStalledDecoder is the boundary case (HISS-02):
// a decoder that never returns frames, EOF, or an error must not spin
// streamMono forever.
func TestStreamMono_BoundedAgainstStalledDecoder(t *testing.T) {
	t.Parallel()
	a := &analyzer{}
	st := stalledStream{ch: 1}
	read := make([]float32, decodeChunkFrames)
	err := a.streamMono(context.Background(), st, read, 1, 3, func(int64, float32) {})
	if err == nil {
		t.Fatal("streamMono() = nil, want bound-exceeded error")
	}
}

// TestLoudness_BoundedAgainstStalledDecoder is the boundary case (HISS-02)
// for the loudness loop's independent chunk-bound copy.
func TestLoudness_BoundedAgainstStalledDecoder(t *testing.T) {
	t.Parallel()
	a := &analyzer{opts: Options{MaxDecodedBytes: 4}} // maxFrames = 4/(1*4) = 1
	st := stalledStream{ch: 1}
	if _, err := a.loudness(context.Background(), st); err == nil {
		t.Fatal("loudness() = nil, want bound-exceeded error")
	}
}

func TestOptionsWithDefaults(t *testing.T) {
	t.Parallel()
	got := Options{}.withDefaults()
	if got.MaxDecodedBytes != DefaultMaxDecodedBytes {
		t.Errorf("MaxDecodedBytes = %d, want %d", got.MaxDecodedBytes, DefaultMaxDecodedBytes)
	}
	if got.DefaultPeakBuckets != DefaultPeakBuckets {
		t.Errorf("DefaultPeakBuckets = %d, want %d", got.DefaultPeakBuckets, DefaultPeakBuckets)
	}
	if got.Backend != "pureGo" {
		t.Errorf("Backend = %q, want pureGo", got.Backend)
	}
	// Explicit values survive.
	custom := Options{MaxDecodedBytes: 99, DefaultPeakBuckets: 7, Backend: "x"}.withDefaults()
	if custom.MaxDecodedBytes != 99 || custom.DefaultPeakBuckets != 7 || custom.Backend != "x" {
		t.Errorf("explicit options overwritten: %+v", custom)
	}
}

func TestSniff(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		head []byte
		hint Format
		want Format
	}{
		{"ogg", []byte("OggS\x00\x00\x00\x00\x00\x00\x00\x00"), "", FormatOGG},
		{"flac", []byte("fLaC\x00\x00\x00\x00\x00\x00\x00\x00"), "", FormatFLAC},
		{"wav", []byte("RIFF\x00\x00\x00\x00WAVE"), "", FormatWAV},
		{"aiff", []byte("FORM\x00\x00\x00\x00AIFF"), "", FormatAIFF},
		{"aifc", []byte("FORM\x00\x00\x00\x00AIFC"), "", FormatAIFF},
		{"mp3-id3", []byte("ID3\x04\x00\x00\x00\x00\x00\x00\x00\x00"), "", FormatMP3},
		{"mp3-sync", []byte{0xFF, 0xFB, 0x90, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "", FormatMP3},
		{"hint-fallback", []byte("zzzzzzzzzzzz"), FormatWAV, FormatWAV},
		{"unknown", []byte("zzzzzzzzzzzz"), "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sniff(tt.head, tt.hint); got != tt.want {
				t.Errorf("sniff(%q) = %q, want %q", tt.head, got, tt.want)
			}
		})
	}
}

func TestClampUnit(t *testing.T) {
	t.Parallel()
	cases := map[float32]float32{2: 1, -2: -1, 0.5: 0.5, -0.5: -0.5, 0: 0}
	for in, want := range cases {
		if got := clampUnit(in); got != want {
			t.Errorf("clampUnit(%f) = %f, want %f", in, got, want)
		}
	}
}

func TestIntSampleToFloat(t *testing.T) {
	t.Parallel()
	if got := intSampleToFloat(16384, 16); math.Abs(float64(got)-0.5) > 1e-4 {
		t.Errorf("16-bit half-scale = %f, want ~0.5", got)
	}
	if got := intSampleToFloat(0, 16); got != 0 {
		t.Errorf("zero sample = %f, want 0", got)
	}
	if got := intSampleToFloat(5, 1); got != 0 {
		t.Errorf("degenerate bit depth = %f, want 0", got)
	}
}

func TestMonoMix(t *testing.T) {
	t.Parallel()
	if got := monoMix([]float32{0.5}); got != 0.5 {
		t.Errorf("mono passthrough = %f, want 0.5", got)
	}
	if got := monoMix([]float32{1, -1}); got != 0 {
		t.Errorf("stereo average = %f, want 0", got)
	}
}

func TestResamplerPassthrough(t *testing.T) {
	t.Parallel()
	// Same src/dst rate: stereo interleave is preserved 1:1.
	rs := newStereoResampler(targetRate)
	in := []float32{0.1, 0.2, 0.3, 0.4}
	out := rs.process(in, 2)
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("out[%d] = %f, want %f", i, out[i], in[i])
		}
	}
}

func TestResamplerDownConvertsRate(t *testing.T) {
	t.Parallel()
	// 96k -> 48k roughly halves the output frame count.
	rs := newStereoResampler(96000)
	const frames = 9600 // 0.1 s at 96k
	in := make([]float32, frames*2)
	for f := range frames {
		v := float32(math.Sin(2 * math.Pi * float64(f) / 100))
		in[f*2], in[f*2+1] = v, v
	}
	out := rs.process(in, 2)
	gotFrames := len(out) / 2
	if gotFrames < frames/2-50 || gotFrames > frames/2+50 {
		t.Errorf("downsampled frames = %d, want ~%d", gotFrames, frames/2)
	}
}

func TestResamplerMonoUpmix(t *testing.T) {
	t.Parallel()
	// Mono input must be duplicated into both stereo channels.
	rs := newStereoResampler(targetRate)
	out := rs.process([]float32{0.25, 0.75}, 1)
	if len(out) != 4 {
		t.Fatalf("len = %d, want 4", len(out))
	}
	if out[0] != 0.25 || out[1] != 0.25 || out[2] != 0.75 || out[3] != 0.75 {
		t.Errorf("mono upmix = %v, want [0.25 0.25 0.75 0.75]", out)
	}
}

func TestFramesFor(t *testing.T) {
	t.Parallel()
	if got := framesFor(Info{SampleRate: 48000, Duration: time.Second}); got != 48000 {
		t.Errorf("framesFor = %d, want 48000", got)
	}
	if got := framesFor(Info{SampleRate: 0, Duration: time.Second}); got != 0 {
		t.Errorf("framesFor with zero rate = %d, want 0", got)
	}
}

func TestPeakAccumulator(t *testing.T) {
	t.Parallel()
	// Two buckets over four frames: first two samples vs. last two.
	acc := newPeakAccumulator(2, 4)
	acc.add(0, 0.2)
	acc.add(1, -0.4)
	acc.add(2, 0.8)
	acc.add(3, -0.1)
	var ps PeakSet
	ps.Min = make([]float32, 2)
	ps.Max = make([]float32, 2)
	acc.finish(&ps)
	if ps.Max[0] != 0.2 || ps.Min[0] != -0.4 {
		t.Errorf("bucket0 min/max = %f/%f, want -0.4/0.2", ps.Min[0], ps.Max[0])
	}
	if ps.Max[1] != 0.8 || ps.Min[1] != -0.1 {
		t.Errorf("bucket1 min/max = %f/%f, want -0.1/0.8", ps.Min[1], ps.Max[1])
	}
}
