// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package safety

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"log/slog"
	"math"
	"testing"
)

func TestDimensionsExceed(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name               string
		width, height, max int
		want               bool
	}{
		{name: "below", width: 10, height: 10, max: 101, want: false},
		{name: "exact", width: 10, height: 10, max: 100, want: false},
		{name: "over", width: 10, height: 11, max: 100, want: true},
		{name: "multiplication overflow", width: math.MaxInt, height: 2, max: math.MaxInt, want: true},
		{name: "zero width", width: 0, height: 10, max: 100, want: true},
		{name: "zero cap fails closed", width: 1, height: 1, max: 0, want: true},
		{name: "negative cap fails closed", width: 1, height: 1, max: -1, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := dimensionsExceed(tt.width, tt.height, tt.max); got != tt.want {
				t.Fatalf("dimensionsExceed(%d, %d, %d) = %t, want %t", tt.width, tt.height, tt.max, got, tt.want)
			}
		})
	}
}

func TestNewStripperDefaultsNonPositivePixelCap(t *testing.T) {
	t.Parallel()
	for _, maxPixels := range []int{0, -1} {
		got, ok := newStripper(
			Options{Strip: StripOptions{MaxPixels: maxPixels}},
			slog.New(slog.DiscardHandler),
		).(*stripper)
		if !ok {
			t.Fatal("newStripper returned an unexpected implementation")
		}
		if got.opts.MaxPixels != defaultStripMaxPixels {
			t.Fatalf("MaxPixels = %d; want default %d", got.opts.MaxPixels, defaultStripMaxPixels)
		}
	}
}

func TestNewStripper_NilLoggerHandlesUnreadableOrientation(t *testing.T) {
	t.Parallel()
	s, ok := newStripper(Options{}, nil).(*stripper)
	if !ok {
		t.Fatal("newStripper returned an unexpected implementation")
	}
	got := s.applyOrientation(context.Background(), image.NewRGBA(image.Rect(0, 0, 1, 1)), []byte("bad"))
	if got == nil {
		t.Fatal("applyOrientation returned nil image")
	}
}

func TestStripStaticHonorsCancellationAfterDecode(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &stripper{
		opts:   StripOptions{JPEGQuality: 85},
		logger: slog.New(slog.DiscardHandler),
	}
	_, _, err := s.stripStatic(ctx, raw.Bytes(), "png")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stripStatic error = %v; want context.Canceled", err)
	}
}

func TestStripGIFHonorsCancellationAfterEncode(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	frame := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black})
	if err := gif.EncodeAll(&raw, &gif.GIF{
		Image: []*image.Paletted{frame}, Delay: []int{0},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := &cancelOnSecondErrContext{Context: context.Background()}
	s := &stripper{logger: slog.New(slog.DiscardHandler)}
	_, _, err := s.stripGIF(ctx, raw.Bytes())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stripGIF error = %v; want context.Canceled", err)
	}
}

type cancelOnSecondErrContext struct {
	context.Context
	calls int
}

func (c *cancelOnSecondErrContext) Err() error {
	c.calls++
	if c.calls >= 2 {
		return context.Canceled
	}
	return nil
}
