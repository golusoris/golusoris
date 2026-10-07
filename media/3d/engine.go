//go:build linux && cgo

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package threed

import (
	"fmt"
	"time"

	"github.com/g3n/engine/app"
	"github.com/g3n/engine/renderer"
)

// App wraps a g3n application window.
type App struct {
	a *app.Application
	r *renderer.Renderer
}

// NewApp returns the g3n application singleton wired with default shaders.
//
// g3n v0.2 manages a single global window via app.App(); window title and size
// are owned by g3n and are no longer constructor parameters.
func NewApp() (*App, error) {
	a := app.App()
	r := renderer.NewRenderer(a.Gls())
	if err := r.AddDefaultShaders(); err != nil {
		return nil, fmt.Errorf("3d: add shaders: %w", err)
	}
	return &App{a: a, r: r}, nil
}

// Run starts the render loop with scene as the root node and blocks until the
// window closes. It returns the first frame that failed to render, if any.
func (a *App) Run(scene *Scene) error {
	var renderErr error
	a.a.Run(func(rend *renderer.Renderer, _ time.Duration) {
		if err := rend.Render(scene, nil); err != nil && renderErr == nil {
			renderErr = fmt.Errorf("3d: render: %w", err)
		}
	})
	return renderErr
}
