package audio_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/golusoris/golusoris/media/audio"
)

// FuzzProbe drives the untrusted-bytes header path: it must never panic and
// must always return a non-nil error or a well-formed Info for any input.
func FuzzProbe(f *testing.F) {
	for _, name := range []string{fixWAV, fixMP3, fixOGG, fixFLAC, fixAIFF} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			f.Fatalf("read seed %s: %v", name, err)
		}
		f.Add(b)
	}
	f.Add([]byte("RIFF\x00\x00\x00\x00WAVE"))
	f.Add([]byte("fLaC"))
	f.Add([]byte("OggS\x00"))
	f.Add([]byte{0xFF, 0xFB})
	f.Add([]byte(nil))

	a, err := audio.NewAnalyzer(audio.Options{MaxDecodedBytes: 1 << 20}, discardLogger())
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, hint := range []audio.Format{"", audio.FormatWAV, audio.FormatMP3} {
			info, perr := a.Probe(context.Background(), bytes.NewReader(data), hint)
			if perr == nil && (info.SampleRate < 0 || info.Channels < 0) {
				t.Fatalf("negative metadata: %+v", info)
			}
		}
	})
}
