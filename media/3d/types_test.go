// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package threed_test

import (
	"testing"

	threed "github.com/golusoris/golusoris/media/3d"
)

func TestNewScene(t *testing.T) {
	t.Parallel()
	if scene := threed.NewScene(); scene == nil {
		t.Fatal("NewScene returned nil")
	}
}
