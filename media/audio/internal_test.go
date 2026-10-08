// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"testing"
	"time"

	goaudio "github.com/go-audio/audio"
)

// stalledStream never advances or terminates: read always returns 0 frames
// and a nil error. It exercises the no-progress guard against a decoder stuck
// without EOF.
type stalledStream struct {
	ch int
}

type fixedStream struct {
	channels int
	samples  int
	err      error
}

type chunkedStream struct {
	streamInfo Info
	chunks     [][]float32
	next       int
}

type fixedIntBufferReader struct {
	samples []int
}

func (r fixedIntBufferReader) PCMBuffer(buf *goaudio.IntBuffer) (int, error) {
	return copy(buf.Data, r.samples), nil
}

func (s fixedStream) info() Info { return Info{Channels: s.channels, SampleRate: 48000} }
func (s fixedStream) read(_ []float32) (int, error) {
	return s.samples, s.err
}

func (s *chunkedStream) info() Info { return s.streamInfo }
func (s *chunkedStream) read(dst []float32) (int, error) {
	if s.next >= len(s.chunks) {
		return 0, io.EOF
	}
	chunk := s.chunks[s.next]
	s.next++
	n := copy(dst, chunk)
	if n != len(chunk) {
		return n, errors.New("test chunk exceeds decode buffer")
	}
	if s.next == len(s.chunks) {
		return n, io.EOF
	}
	return n, nil
}

func (s stalledStream) info() Info                  { return Info{Channels: s.ch, SampleRate: 48000} }
func (stalledStream) read(_ []float32) (int, error) { return 0, nil }

func TestDecodeChunkLimit(t *testing.T) {
	t.Parallel()
	if got := decodeChunkLimit(0); got != 1 {
		t.Errorf("decodeChunkLimit(0) = %d, want 1", got)
	}
	if got := decodeChunkLimit(-5); got != decodeChunkNoLimitGuard {
		t.Errorf("decodeChunkLimit(-5) = %d, want %d", got, decodeChunkNoLimitGuard)
	}
	if got := decodeChunkLimit(10); got != 11 {
		t.Errorf("decodeChunkLimit(10) = %d, want 11", got)
	}
}

func TestBoundedReaderExactLimit(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		data    string
		wantErr error
	}{
		{name: "below", data: "123", wantErr: nil},
		{name: "exact", data: "1234", wantErr: nil},
		{name: "over", data: "12345", wantErr: ErrInputTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := io.ReadAll(&boundedReader{r: bytes.NewBufferString(tt.data), remaining: 4})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReadAll() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBoundedReaderStopsAfterCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	source := &trackingByteReader{data: []byte("1234")}
	reader := &boundedReader{
		check:     contextCheck(ctx),
		r:         source,
		remaining: 4,
	}
	if _, err := reader.Read(make([]byte, 1)); err != nil {
		t.Fatalf("first Read() error = %v", err)
	}
	cancel()
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read() after cancellation error = %v, want context.Canceled", err)
	}
	if source.reads != 1 {
		t.Fatalf("underlying reads = %d, want 1", source.reads)
	}
}

type trackingByteReader struct {
	data  []byte
	reads int
}

func (r *trackingByteReader) Read(p []byte) (int, error) {
	r.reads++
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestValidateAudioInfoChannelBoundary(t *testing.T) {
	t.Parallel()
	base := Info{SampleRate: 48000, BitDepth: 16}
	for _, tt := range []struct {
		name     string
		channels int
		wantErr  error
	}{
		{name: "one", channels: 1},
		{name: "exact maximum", channels: DefaultMaxChannels},
		{name: "zero", channels: 0, wantErr: ErrCorrupt},
		{name: "over maximum", channels: DefaultMaxChannels + 1, wantErr: ErrCorrupt},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info := base
			info.Channels = tt.channels
			if err := validateAudioInfo(info, DefaultMaxChannels); !errors.Is(err, tt.wantErr) {
				t.Fatalf("validateAudioInfo(channels=%d) error = %v, want %v", tt.channels, err, tt.wantErr)
			}
		})
	}
}

func TestReadPCMChunkDecodedByteBoundary(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		samples      int
		wantErr      error
		wantConsumed int
	}{
		{name: "exact", samples: 2, wantConsumed: 2},
		{name: "over", samples: 3, wantErr: ErrInputTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var frameIdx int64
			consumed := 0
			_, err := readPCMChunk(
				fixedStream{channels: 1, samples: tt.samples, err: io.EOF},
				make([]float32, 3),
				1,
				2,
				&frameIdx,
				func(_ []float32, frames int, _ int64) error {
					consumed += frames
					return nil
				},
			)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("readPCMChunk() error = %v, want %v", err, tt.wantErr)
			}
			if consumed != tt.wantConsumed {
				t.Fatalf("consumed = %d, want %d", consumed, tt.wantConsumed)
			}
		})
	}
}

func TestReadPCMChunkRejectsDecoderWithoutProgress(t *testing.T) {
	t.Parallel()
	var frameIdx int64
	done, err := readPCMChunk(
		stalledStream{ch: 1},
		make([]float32, decodeChunkFrames),
		1,
		math.MaxInt64,
		&frameIdx,
		func(_ []float32, _ int, _ int64) error { return nil },
	)
	if done {
		t.Fatal("readPCMChunk() done = true, want false")
	}
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("readPCMChunk() error = %v, want io.ErrNoProgress", err)
	}
}

// TestStreamMonoRejectsStalledDecoder is the boundary case (HISS-02): a
// decoder that never returns frames, EOF, or an error must not spin forever.
func TestStreamMonoRejectsStalledDecoder(t *testing.T) {
	t.Parallel()
	a := &analyzer{}
	st := stalledStream{ch: 1}
	read := make([]float32, decodeChunkFrames)
	err := a.streamMono(context.Background(), st, read, 1, 3, func(int64, float32) {})
	if err == nil {
		t.Fatal("streamMono() = nil, want bound-exceeded error")
	}
}

func TestStreamMono_CancelledBeforeDecode(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &analyzer{}
	err := a.streamMono(ctx, stalledStream{ch: 1}, make([]float32, decodeChunkFrames), 1, 3, func(int64, float32) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("streamMono() error = %v, want context.Canceled", err)
	}
}

// TestLoudnessRejectsStalledDecoder is the loudness boundary case for the
// shared PCM chunk loop.
func TestLoudnessRejectsStalledDecoder(t *testing.T) {
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
	if got.MaxInputBytes != DefaultMaxInputBytes {
		t.Errorf("MaxInputBytes = %d, want %d", got.MaxInputBytes, DefaultMaxInputBytes)
	}
	if got.MaxDecodedBytes != DefaultMaxDecodedBytes {
		t.Errorf("MaxDecodedBytes = %d, want %d", got.MaxDecodedBytes, DefaultMaxDecodedBytes)
	}
	if got.DefaultPeakBuckets != DefaultPeakBuckets {
		t.Errorf("DefaultPeakBuckets = %d, want %d", got.DefaultPeakBuckets, DefaultPeakBuckets)
	}
	if got.MaxPeakBuckets != DefaultMaxPeakBuckets {
		t.Errorf("MaxPeakBuckets = %d, want %d", got.MaxPeakBuckets, DefaultMaxPeakBuckets)
	}
	if got.MaxChannels != DefaultMaxChannels {
		t.Errorf("MaxChannels = %d, want %d", got.MaxChannels, DefaultMaxChannels)
	}
	if got.Backend != "pureGo" {
		t.Errorf("Backend = %q, want pureGo", got.Backend)
	}
	// Explicit values survive.
	custom := Options{
		MaxInputBytes:      98,
		MaxDecodedBytes:    99,
		MaxChannels:        4,
		DefaultPeakBuckets: 7,
		MaxPeakBuckets:     8,
		Backend:            "x",
	}.withDefaults()
	if custom.MaxInputBytes != 98 || custom.MaxDecodedBytes != 99 || custom.MaxChannels != 4 ||
		custom.DefaultPeakBuckets != 7 || custom.MaxPeakBuckets != 8 || custom.Backend != "x" {
		t.Errorf("explicit options overwritten: %+v", custom)
	}
}

func TestOpenMP3BoundsSeekableLengthScan(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/sine.mp3")
	if err != nil {
		t.Fatalf("read mp3 fixture: %v", err)
	}
	if _, err = boundInput(context.Background(), bytes.NewReader(data), 4096); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("boundInput(over limit) error = %v; want ErrInputTooLarge", err)
	}
	source, err := boundInput(context.Background(), bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("boundInput(exact limit): %v", err)
	}
	stream, err := openMP3(source)
	if err != nil {
		t.Fatalf("openMP3(exact limit): %v", err)
	}
	if stream.info().Duration <= 0 {
		t.Fatal("openMP3(exact limit) lost seekable duration")
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

func TestSeekableEightBitPCMUsesUnsignedMidpoint(t *testing.T) {
	t.Parallel()
	for _, format := range []Format{FormatWAV, FormatAIFF} {
		stream := newSeekableStream(
			fixedIntBufferReader{samples: []int{0, 128, 255}},
			Info{Format: format, SampleRate: 8000, Channels: 1, BitDepth: 8},
			3*8,
		)
		got := make([]float32, 3)
		n, err := stream.read(got)
		if err != nil {
			t.Fatalf("%s read() error = %v", format, err)
		}
		if n != len(got) {
			t.Fatalf("%s read() samples = %d, want %d", format, n, len(got))
		}
		want := []float32{-1, 0, 127.0 / 128.0}
		for i := range want {
			if math.Abs(float64(got[i]-want[i])) > 1e-6 {
				t.Errorf("%s sample %d = %f, want %f", format, i, got[i], want[i])
			}
		}
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
	out, err := rs.process(in, 2, int64(len(in)/2))
	if err != nil {
		t.Fatalf("process() error = %v", err)
	}
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
	out, err := rs.process(in, 2, frames)
	if err != nil {
		t.Fatalf("process() error = %v", err)
	}
	gotFrames := len(out) / 2
	if gotFrames < frames/2-50 || gotFrames > frames/2+50 {
		t.Errorf("downsampled frames = %d, want ~%d", gotFrames, frames/2)
	}
}

func TestResamplerRampPreservesPhaseAcrossChunks(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		rate   int
		chunks [][]float32
		want   []float32
	}{
		{
			name:   "upsample one-frame chunks",
			rate:   targetRate / 2,
			chunks: [][]float32{{0}, {1}, {2}, {3}},
			want:   []float32{0, 0.5, 1, 1.5, 2, 2.5, 3, 3},
		},
		{
			name:   "identity one-frame chunks",
			rate:   targetRate,
			chunks: [][]float32{{0}, {1}, {2}, {3}},
			want:   []float32{0, 1, 2, 3},
		},
		{
			name:   "downsample one-frame chunks",
			rate:   targetRate * 2,
			chunks: [][]float32{{0}, {1}, {2}, {3}},
			want:   []float32{0, 2},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := processMonoChunks(t, newStereoResampler(tt.rate), tt.chunks)
			assertStereoSamples(t, got, tt.want)
		})
	}
}

func TestResamplerImpulseMatchesAcrossChunkBoundaries(t *testing.T) {
	t.Parallel()
	source := []float32{0, 0, 1, 0, 0}
	oneShot := processMonoChunks(t, newStereoResampler(targetRate/2), [][]float32{source})
	chunked := processMonoChunks(t, newStereoResampler(targetRate/2), [][]float32{
		source[:2], source[2:3], source[3:],
	})
	want := []float32{0, 0, 0, 0.5, 1, 0.5, 0, 0, 0, 0}
	assertStereoSamples(t, oneShot, want)
	assertStereoSamples(t, chunked, want)
}

func TestResamplerChunkingDoesNotChangeOutput(t *testing.T) {
	t.Parallel()
	source := make([]float32, 17)
	for frame := range source {
		source[frame] = float32(math.Sin(float64(frame)*0.7) + float64(frame)/20)
	}
	for _, rate := range []int{targetRate / 2, 44100, targetRate * 2} {
		wantStereo := processMonoChunks(t, newStereoResampler(rate), [][]float32{source})
		wantMono := make([]float32, len(wantStereo)/2)
		for frame := range wantMono {
			wantMono[frame] = wantStereo[frame*2]
		}
		for _, chunkFrames := range []int{1, 2, 5} {
			chunks := make([][]float32, 0, (len(source)+chunkFrames-1)/chunkFrames)
			for first := 0; first < len(source); first += chunkFrames {
				last := min(first+chunkFrames, len(source))
				chunks = append(chunks, source[first:last])
			}
			got := processMonoChunks(t, newStereoResampler(rate), chunks)
			assertStereoSamples(t, got, wantMono)
		}
	}
}

func processMonoChunks(t *testing.T, rs *stereoResampler, chunks [][]float32) []float32 {
	t.Helper()
	var out []float32
	for _, chunk := range chunks {
		part, err := rs.process(chunk, 1, 1<<20)
		if err != nil {
			t.Fatalf("process() error = %v", err)
		}
		out = append(out, part...)
	}
	flushed, err := rs.flush(1 << 20)
	if err != nil {
		t.Fatalf("flush() error = %v", err)
	}
	return append(out, flushed...)
}

func assertStereoSamples(t *testing.T, got []float32, wantMono []float32) {
	t.Helper()
	if len(got) != len(wantMono)*2 {
		t.Fatalf("output samples = %d, want %d: %v", len(got), len(wantMono)*2, got)
	}
	for frame, want := range wantMono {
		left, right := got[frame*2], got[frame*2+1]
		if math.Abs(float64(left-want)) > 1e-6 || math.Abs(float64(right-want)) > 1e-6 {
			t.Fatalf("frame %d = (%f, %f), want (%f, %f)", frame, left, right, want, want)
		}
	}
}

func TestResamplerMonoUpmix(t *testing.T) {
	t.Parallel()
	// Mono input must be duplicated into both stereo channels.
	rs := newStereoResampler(targetRate)
	out, err := rs.process([]float32{0.25, 0.75}, 1, 2)
	if err != nil {
		t.Fatalf("process() error = %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("len = %d, want 4", len(out))
	}
	if out[0] != 0.25 || out[1] != 0.25 || out[2] != 0.75 || out[3] != 0.75 {
		t.Errorf("mono upmix = %v, want [0.25 0.25 0.75 0.75]", out)
	}
}

func TestLoudnessDecodeBufferBoundsUpsampling(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		sampleRate int
		wantFrames int
	}{
		{name: "target rate", sampleRate: targetRate, wantFrames: decodeChunkFrames},
		{name: "half rate", sampleRate: targetRate / 2, wantFrames: decodeChunkFrames / 2},
		{name: "minimum positive rate", sampleRate: 1, wantFrames: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := loudnessDecodeBuffer(1, tt.sampleRate, DefaultMaxDecodedBytes)
			if err != nil {
				t.Fatalf("loudnessDecodeBuffer() error = %v", err)
			}
			if len(got) != tt.wantFrames {
				t.Fatalf("len(buffer) = %d, want %d", len(got), tt.wantFrames)
			}
		})
	}
}

func TestResamplerRejectsOutputBeyondLimitBeforeLargeAllocation(t *testing.T) {
	t.Parallel()
	rs := newStereoResampler(1)
	out, err := rs.process([]float32{0.5}, 1, 1)
	if err != nil {
		t.Fatalf("process() error = %v", err)
	}
	_, err = rs.flush(1 - int64(len(out)/2))
	if !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("flush() error = %v, want ErrInputTooLarge", err)
	}
}

func TestLoudnessStereoOutputHonorsByteBudget(t *testing.T) {
	t.Parallel()
	a := &analyzer{opts: Options{MaxDecodedBytes: 8}}
	_, err := a.loudness(context.Background(), fixedStream{channels: 1, samples: 2, err: io.EOF})
	if !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("loudness() error = %v; want ErrInputTooLarge", err)
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
	if got := framesFor(Info{
		SampleRate: int(^uint(0) >> 1),
		Duration:   time.Duration(1<<63 - 1),
	}); got != int64(^uint64(0)>>1) {
		t.Errorf("framesFor overflow = %d, want saturation", got)
	}
}

func TestDurationForFramesSaturates(t *testing.T) {
	t.Parallel()
	if got := durationForFrames(48000, 48000); got != time.Second {
		t.Fatalf("durationForFrames(normal) = %s; want 1s", got)
	}
	if got := durationForUnsignedFrames(^uint64(0), 1); got != time.Duration(1<<63-1) {
		t.Fatalf("durationForUnsignedFrames(overflow) = %s; want saturation", got)
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

func TestWaveformUnknownDurationDistributesMultipleChunks(t *testing.T) {
	t.Parallel()
	stream := &chunkedStream{
		streamInfo: Info{SampleRate: 8, Channels: 1},
		chunks: [][]float32{
			{-1, 0.5},
			{-0.75, 0.25},
			{-0.5, 1},
			{-0.25, 0.75},
		},
	}
	a := &analyzer{opts: Options{MaxDecodedBytes: 64}}
	got, err := a.waveform(context.Background(), stream, 4)
	if err != nil {
		t.Fatalf("waveform() error = %v", err)
	}
	wantMin := []float32{-1, -0.75, -0.5, -0.25}
	wantMax := []float32{0.5, 0.25, 1, 0.75}
	for bucket := range 4 {
		if got.Min[bucket] != wantMin[bucket] || got.Max[bucket] != wantMax[bucket] {
			t.Errorf(
				"bucket %d min/max = %f/%f, want %f/%f",
				bucket,
				got.Min[bucket],
				got.Max[bucket],
				wantMin[bucket],
				wantMax[bucket],
			)
		}
	}
}

func TestStreamingPeakAccumulatorBoundsSummaries(t *testing.T) {
	t.Parallel()
	const buckets = 4
	acc := newStreamingPeakAccumulator(buckets)
	for frame := range int64(1 << 16) {
		acc.add(frame, float32(frame%7)/3-1)
		if len(acc.bins) > buckets*2 {
			t.Fatalf("summary bins = %d, want <= %d", len(acc.bins), buckets*2)
		}
	}
}

func TestPeakBucketIndexDoesNotOverflow(t *testing.T) {
	t.Parallel()
	const totalFrames = int64(1<<63 - 1)
	got := peakBucketIndex(totalFrames-1, DefaultMaxPeakBuckets, totalFrames)
	if got != DefaultMaxPeakBuckets-1 {
		t.Fatalf("peakBucketIndex() = %d; want %d", got, DefaultMaxPeakBuckets-1)
	}
}
