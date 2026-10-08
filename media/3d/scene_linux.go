//go:build linux && cgo

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package threed

import "github.com/g3n/engine/core"

// Scene wraps a g3n core.Node as the scene root.
type Scene = core.Node

// NewScene creates a new scene root node.
func NewScene() *Scene {
	return core.NewNode()
}
