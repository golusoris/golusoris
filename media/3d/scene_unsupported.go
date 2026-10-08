//go:build !linux || !cgo

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package threed

// Scene is an opaque placeholder when the native renderer is unavailable.
type Scene struct{}

// NewScene creates an inert scene placeholder for portable construction paths.
func NewScene() *Scene {
	return &Scene{}
}
