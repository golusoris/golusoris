// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package game_test

import (
	"testing"

	"github.com/golusoris/golusoris/media/game"
)

func TestNewImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		width      int
		height     int
		shouldFail bool
	}{
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

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.shouldFail {
				defer func() {
					if r := recover(); r == nil {
						t.Fatal("expected panic for non-positive dimensions")
					}
				}()
				_ = game.NewImage(tc.width, tc.height)
				return
			}

			img := game.NewImage(tc.width, tc.height)
			if img == nil {
				t.Fatal("expected non-nil image")
			}
			bounds := img.Bounds()
			if bounds.Dx() != tc.width || bounds.Dy() != tc.height {
				t.Fatalf("bounds = (%d, %d), want (%d, %d)", bounds.Dx(), bounds.Dy(), tc.width, tc.height)
			}
		})
	}
}
