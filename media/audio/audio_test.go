package audio_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golusoris/golusoris/media/audio"
)

// fixtures live in testdata/, generated from a 1 kHz tone (see AGENTS.md).
const (
	fixWAV         = "sine.wav"
	fixMP3         = "sine.mp3"
	fixOGG         = "sine.ogg"
	fixFLAC        = "sine.flac"
	fixAIFF        = "sine.aiff"
	fixSineSilence = "sine_silence.wav" // 1.5 s tone + 1.5 s silence
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newAnalyzer(t *testing.T, opts audio.Options) audio.Analyzer {
	t.Helper()
	a, err := audio.NewAnalyzer(opts, discardLogger())
	if err != nil {
		t.Fatalf("NewAnalyzer: %v", err)
	}
	return a
}

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestNewAnalyzer(t *testing.T) {
	t.Parallel()
	if _, err := audio.NewAnalyzer(audio.Options{}, nil); err == nil {
		t.Fatal("expected error for nil logger")
	}
	// A non-positive cap is lenient: it falls back to the package default.
	if _, err := audio.NewAnalyzer(audio.Options{MaxDecodedBytes: -1}, discardLogger()); err != nil {
		t.Fatalf("negative MaxDecodedBytes should fall back to default: %v", err)
	}
	if _, err := audio.NewAnalyzer(audio.Options{}, discardLogger()); err != nil {
		t.Fatalf("zero options should be valid: %v", err)
	}
}

func TestProbe(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		file   string
		format audio.Format
	}{
		{"wav", fixWAV, audio.FormatWAV},
		{"mp3", fixMP3, audio.FormatMP3},
		{"ogg", fixOGG, audio.FormatOGG},
		{"flac", fixFLAC, audio.FormatFLAC},
		{"aiff", fixAIFF, audio.FormatAIFF},
	}
	a := newAnalyzer(t, audio.Options{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info, err := a.Probe(context.Background(), openFixture(t, tt.file), "")
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if info.Format != tt.format {
				t.Errorf("Format = %q, want %q", info.Format, tt.format)
			}
			if info.SampleRate != 44100 {
				t.Errorf("SampleRate = %d, want 44100", info.SampleRate)
			}
			if info.Channels != 2 {
				t.Errorf("Channels = %d, want 2", info.Channels)
			}
			// Fixtures are ~3 s; compressed formats add small encoder padding.
			if info.Duration < 2900*time.Millisecond || info.Duration > 3200*time.Millisecond {
				t.Errorf("Duration = %s, want ~3s", info.Duration)
			}
		})
	}
}

func TestProbeHintWins_OverSniffOnlyWhenInconclusive(t *testing.T) {
	t.Parallel()
	// Magic-byte sniff must override a lying hint: MP3 bytes, hint says WAV.
	a := newAnalyzer(t, audio.Options{})
	info, err := a.Probe(context.Background(), openFixture(t, fixMP3), audio.FormatWAV)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.Format != audio.FormatMP3 {
		t.Errorf("sniff should win: Format = %q, want mp3", info.Format)
	}
}

func TestProbeMaxDuration(t *testing.T) {
	t.Parallel()
	a := newAnalyzer(t, audio.Options{MaxDuration: time.Second})
	_, err := a.Probe(context.Background(), openFixture(t, fixWAV), audio.FormatWAV)
	if !errors.Is(err, audio.ErrTooLong) {
		t.Fatalf("expected ErrTooLong, got %v", err)
	}
}

func TestWaveform(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file string
	}{
		{"wav", fixWAV},
		{"mp3", fixMP3},
		{"ogg", fixOGG},
		{"flac", fixFLAC},
		{"aiff", fixAIFF},
	}
	a := newAnalyzer(t, audio.Options{})
	const buckets = 128
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ps, err := a.Waveform(context.Background(), openFixture(t, tt.file), "", buckets)
			if err != nil {
				t.Fatalf("Waveform: %v", err)
			}
			if ps.Buckets != buckets || len(ps.Min) != buckets || len(ps.Max) != buckets {
				t.Fatalf("buckets = %d, len(min) = %d, len(max) = %d, want %d",
					ps.Buckets, len(ps.Min), len(ps.Max), buckets)
			}
			var sawSignal bool
			for i := range buckets {
				if ps.Min[i] < -1 || ps.Max[i] > 1 {
					t.Fatalf("bucket %d out of [-1,1]: min=%f max=%f", i, ps.Min[i], ps.Max[i])
				}
				if ps.Min[i] > ps.Max[i] {
					t.Fatalf("bucket %d: min %f > max %f", i, ps.Min[i], ps.Max[i])
				}
				if ps.Max[i] > 0 {
					sawSignal = true
				}
			}
			if !sawSignal {
				t.Error("expected non-zero peaks for a tone fixture")
			}
		})
	}
}

func TestWaveformDefaultBuckets(t *testing.T) {
	t.Parallel()
	a := newAnalyzer(t, audio.Options{DefaultPeakBuckets: 256})
	ps, err := a.Waveform(context.Background(), openFixture(t, fixWAV), audio.FormatWAV, 0)
	if err != nil {
		t.Fatalf("Waveform: %v", err)
	}
	if ps.Buckets != 256 {
		t.Errorf("Buckets = %d, want 256 (default)", ps.Buckets)
	}
}

func TestWaveformSilentRegion(t *testing.T) {
	t.Parallel()
	// First half is tone, second half is digital silence.
	a := newAnalyzer(t, audio.Options{})
	const buckets = 64
	ps, err := a.Waveform(context.Background(), openFixture(t, fixSineSilence), audio.FormatWAV, buckets)
	if err != nil {
		t.Fatalf("Waveform: %v", err)
	}
	if ps.Max[10] == 0 {
		t.Error("tone region should have non-zero peaks")
	}
	if ps.Max[buckets-5] != 0 || ps.Min[buckets-5] != 0 {
		t.Errorf("silent region should be ~0, got min=%f max=%f",
			ps.Min[buckets-5], ps.Max[buckets-5])
	}
}

func TestLoudness(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file string
	}{
		{"wav", fixWAV},
		{"mp3", fixMP3},
		{"ogg", fixOGG},
		{"flac", fixFLAC},
		{"aiff", fixAIFF},
	}
	a := newAnalyzer(t, audio.Options{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l, err := a.Loudness(context.Background(), openFixture(t, tt.file), "")
			if err != nil {
				t.Fatalf("Loudness: %v", err)
			}
			// The fixture tone measures ~-24 LUFS across every codec; allow a
			// wide band so encoder differences don't make this flaky.
			if l.IntegratedLUFS < -30 || l.IntegratedLUFS > -18 {
				t.Errorf("IntegratedLUFS = %.2f, want ~-24", l.IntegratedLUFS)
			}
			if l.TruePeakDBTP > 0 {
				t.Errorf("TruePeakDBTP = %.2f, want < 0 dBTP", l.TruePeakDBTP)
			}
		})
	}
}

func TestLoudnessCrossCodecConsistency(t *testing.T) {
	t.Parallel()
	// The same tone in WAV (44.1k, resampled) and OGG must measure within 1 LU.
	a := newAnalyzer(t, audio.Options{})
	wav, err := a.Loudness(context.Background(), openFixture(t, fixWAV), audio.FormatWAV)
	if err != nil {
		t.Fatalf("wav: %v", err)
	}
	ogg, err := a.Loudness(context.Background(), openFixture(t, fixOGG), audio.FormatOGG)
	if err != nil {
		t.Fatalf("ogg: %v", err)
	}
	if diff := wav.IntegratedLUFS - ogg.IntegratedLUFS; diff > 1 || diff < -1 {
		t.Errorf("LUFS mismatch wav=%.2f ogg=%.2f (resampler/decoder drift)",
			wav.IntegratedLUFS, ogg.IntegratedLUFS)
	}
}

func TestRobustness(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data []byte
		hint audio.Format
		want error
	}{
		{"zero-byte", nil, "", audio.ErrUnknownFormat},
		{"garbage", []byte("this is definitely not audio data"), "", audio.ErrUnknownFormat},
		// Hint is a trusted fallback (see TestSniff/hint-fallback): garbage routed
		// to the hinted decoder surfaces as a corrupt-input error, not unknown.
		{"garbage-with-hint", []byte("xx"), audio.FormatMP3, audio.ErrCorrupt},
		{"truncated-flac", readFixture(t, fixFLAC)[:40], audio.FormatFLAC, audio.ErrCorrupt},
		{"truncated-mp3", readFixture(t, fixMP3)[:40], audio.FormatMP3, audio.ErrCorrupt},
		{"truncated-ogg", readFixture(t, fixOGG)[:40], audio.FormatOGG, audio.ErrCorrupt},
	}
	a := newAnalyzer(t, audio.Options{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := a.Probe(context.Background(), bytes.NewReader(tt.data), tt.hint)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Probe: got %v, want %v", err, tt.want)
			}
		})
	}
}

func TestInputTooLarge(t *testing.T) {
	t.Parallel()
	// A 1 KiB decode cap is far below the fixture's PCM size.
	a := newAnalyzer(t, audio.Options{MaxDecodedBytes: 1024})
	_, err := a.Waveform(context.Background(), openFixture(t, fixFLAC), audio.FormatFLAC, 32)
	if !errors.Is(err, audio.ErrInputTooLarge) {
		t.Fatalf("Waveform: got %v, want ErrInputTooLarge", err)
	}
}

func TestContextCancel(t *testing.T) {
	t.Parallel()
	a := newAnalyzer(t, audio.Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.Probe(ctx, openFixture(t, fixWAV), audio.FormatWAV)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe with cancelled ctx: got %v, want context.Canceled", err)
	}
}

func TestNeverPanics(t *testing.T) {
	t.Parallel()
	// Decoders must never panic on adversarial input; only return errors.
	a := newAnalyzer(t, audio.Options{})
	seeds := [][]byte{
		nil,
		{0x00},
		[]byte("RIFF\x00\x00\x00\x00WAVE"),
		[]byte("fLaC\x00\x00\x00"),
		[]byte("OggS\x00\x00"),
		[]byte("ID3\x04\x00"),
		{0xFF, 0xFB, 0x00, 0x00},
		bytes.Repeat([]byte{0xFF}, 64),
	}
	for i, seed := range seeds {
		_, _ = a.Probe(context.Background(), bytes.NewReader(seed), "")
		_, _ = a.Waveform(context.Background(), bytes.NewReader(seed), "", 8)
		_, _ = a.Loudness(context.Background(), bytes.NewReader(seed), "")
		_ = i
	}
}
