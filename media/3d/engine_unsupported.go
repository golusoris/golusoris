//go:build !linux || !cgo

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package threed

// App is unavailable without the Linux CGO renderer.
type App struct{}

// NewApp reports that the native renderer is unavailable on this build.
func NewApp() (*App, error) {
	return nil, ErrUnsupported
}

// Run reports that the native renderer is unavailable on this build.
func (a *App) Run(_ *Scene) error {
	return ErrUnsupported
}
