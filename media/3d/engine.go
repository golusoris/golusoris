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
//	app.Run(scene)
package threed

import (
	"fmt"
	"time"

	"github.com/g3n/engine/app"
	"github.com/g3n/engine/core"
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

// Run starts the render loop with scene as the root node.
func (a *App) Run(scene *core.Node) {
	a.a.Run(func(rend *renderer.Renderer, _ time.Duration) {
		_ = rend.Render(scene, nil)
	})
}

// Scene wraps a g3n core.Node as the scene root.
type Scene = core.Node

// NewScene creates a new scene root node.
func NewScene() *core.Node {
	return core.NewNode()
}
