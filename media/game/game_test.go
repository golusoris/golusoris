// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package game

import (
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
)

var resolveWindowTests = []struct {
	name string
	opts Options
	want windowConfig
}{
	{
		name: "positive all fields",
		opts: Options{Title: "Custom", Width: 320, Height: 240, WindowScale: 2, Fullscreen: true, VSync: true},
		want: windowConfig{title: "Custom", width: 640, height: 480, fullscreen: true, vsync: true},
	},
	{
		name: "negative zero values default",
		opts: Options{},
		want: windowConfig{title: "golusoris game", width: 640, height: 480},
	},
	{
		name: "negative zero width only",
		opts: Options{Title: "Width", Height: 240},
		want: windowConfig{title: "Width", width: 640, height: 240},
	},
	{
		name: "negative zero height only",
		opts: Options{Title: "Height", Width: 320},
		want: windowConfig{title: "Height", width: 320, height: 480},
	},
	{
		name: "negative sizes default",
		opts: Options{Width: -320, Height: -240},
		want: windowConfig{title: "golusoris game", width: 640, height: 480},
	},
	{
		name: "negative zero scale preserves size",
		opts: Options{Title: "Unscaled", Width: 320, Height: 240, WindowScale: 0},
		want: windowConfig{title: "Unscaled", width: 320, height: 240},
	},
	{
		name: "negative scale preserves size",
		opts: Options{Title: "Unscaled", Width: 320, Height: 240, WindowScale: -2},
		want: windowConfig{title: "Unscaled", width: 320, height: 240},
	},
	{
		name: "boundary minimal size",
		opts: Options{Width: 1, Height: 1},
		want: windowConfig{title: "golusoris game", width: 1, height: 1},
	},
	{
		name: "boundary fractional scale truncates odd size",
		opts: Options{Width: 641, Height: 481, WindowScale: 0.5},
		want: windowConfig{title: "golusoris game", width: 320, height: 240},
	},
	{
		name: "boundary scale after defaulting",
		opts: Options{WindowScale: 2},
		want: windowConfig{title: "golusoris game", width: 1280, height: 960},
	},
	{
		name: "boundary fractional scale truncates to zero",
		opts: Options{Width: 1, Height: 1, WindowScale: 0.5},
		want: windowConfig{title: "golusoris game", width: 0, height: 0},
	},
}

func TestResolveWindow(t *testing.T) {
	t.Parallel()
	for _, tc := range resolveWindowTests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveWindow(tc.opts); got != tc.want {
				t.Errorf("resolveWindow() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

//nolint:paralleltest // ebiten window settings are process-global.
func TestApplyWindow(t *testing.T) {
	width, height := ebiten.WindowSize()
	fullscreen, vsync := ebiten.IsFullscreen(), ebiten.IsVsyncEnabled()
	t.Cleanup(func() {
		ebiten.SetWindowSize(width, height)
		ebiten.SetFullscreen(fullscreen)
		ebiten.SetVsyncEnabled(vsync)
	})
	tests := []struct {
		name string
		cfg  windowConfig
	}{
		{
			name: "positive custom settings",
			cfg:  windowConfig{title: "Custom", width: 320, height: 240, fullscreen: true, vsync: false},
		},
		{
			name: "boundary default settings",
			cfg:  windowConfig{title: "golusoris game", width: 640, height: 480, fullscreen: false, vsync: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			applyWindow(tc.cfg)
			if gotWidth, gotHeight := ebiten.WindowSize(); gotWidth != tc.cfg.width || gotHeight != tc.cfg.height {
				t.Errorf("window size = (%d, %d), want (%d, %d)", gotWidth, gotHeight, tc.cfg.width, tc.cfg.height)
			}
			if got := ebiten.IsFullscreen(); got != tc.cfg.fullscreen {
				t.Errorf("fullscreen = %t, want %t", got, tc.cfg.fullscreen)
			}
			if got := ebiten.IsVsyncEnabled(); got != tc.cfg.vsync {
				t.Errorf("vsync = %t, want %t", got, tc.cfg.vsync)
			}
		})
	}
}

type newImageTestCase struct {
	name       string
	width      int
	height     int
	shouldFail bool
}

var newImageTests = []newImageTestCase{
	{
		name:       "positive standard image",
		width:      100,
		height:     50,
		shouldFail: false,
	},
	{
		name:       "boundary minimal 1x1 image",
		width:      1,
		height:     1,
		shouldFail: false,
	},
	{
		name:       "boundary non-positive dimensions panic",
		width:      0,
		height:     0,
		shouldFail: true,
	},
}

// checkPanicImageCase asserts that non-positive dimensions panic.
func checkPanicImageCase(t *testing.T, width, height int) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for non-positive dimensions")
		}
	}()
	_ = NewImage(width, height)
}

// checkValidImageCase asserts that valid dimensions produce a properly sized image.
func checkValidImageCase(t *testing.T, width, height int) {
	t.Helper()
	img := NewImage(width, height)
	if img == nil {
		t.Fatal("expected non-nil image")
	}
	bounds := img.Bounds()
	if bounds.Dx() != width || bounds.Dy() != height {
		t.Fatalf("bounds = (%d, %d), want (%d, %d)", bounds.Dx(), bounds.Dy(), width, height)
	}
}

// runNewImageTestCase delegates test execution based on shouldFail flag.
func runNewImageTestCase(t *testing.T, tc newImageTestCase) {
	t.Helper()
	if tc.shouldFail {
		checkPanicImageCase(t, tc.width, tc.height)
		return
	}
	checkValidImageCase(t, tc.width, tc.height)
}

func TestNewImage(t *testing.T) {
	t.Parallel()
	for _, tc := range newImageTests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runNewImageTestCase(t, tc)
		})
	}
}
