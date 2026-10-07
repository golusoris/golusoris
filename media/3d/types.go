// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package threed provides a thin wrapper over g3n/engine for 3D rendering.
//
// This is a separate go.mod sub-module because g3n pulls CGO + OpenGL/GLFW
// drivers that require a GPU and display server.
// Import directly: github.com/golusoris/golusoris/media/3d
//
// # Usage
//
//	app, err := threed.NewApp()
//	scene := threed.NewScene()
//	// add meshes, lights, cameras to scene
//	err = app.Run(scene)
package threed

import (
	"errors"
)

// ErrUnsupported reports that the native renderer is unavailable on this build.
var ErrUnsupported = errors.New("3d: requires Linux with CGO")
