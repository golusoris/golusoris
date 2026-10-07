//go:build !linux || !cgo

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package threed_test

import (
	"errors"
	"testing"

	threed "github.com/golusoris/golusoris/media/3d"
)

func TestRendererReportsUnsupported(t *testing.T) {
	t.Parallel()
	app, err := threed.NewApp()
	if app != nil || !errors.Is(err, threed.ErrUnsupported) {
		t.Fatalf("NewApp() = (%v, %v), want (nil, ErrUnsupported)", app, err)
	}

	var unavailable threed.App
	if err := unavailable.Run(threed.NewScene()); !errors.Is(err, threed.ErrUnsupported) {
		t.Fatalf("Run() error = %v, want ErrUnsupported", err)
	}
}
