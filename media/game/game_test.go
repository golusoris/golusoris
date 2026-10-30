// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package game_test

import (
	"testing"

	"github.com/golusoris/golusoris/media/game"
)

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
	_ = game.NewImage(width, height)
}

// checkValidImageCase asserts that valid dimensions produce a properly sized image.
func checkValidImageCase(t *testing.T, width, height int) {
	t.Helper()
	img := game.NewImage(width, height)
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
