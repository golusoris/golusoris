// Package threed provides a thin wrapper over g3n/engine for 3D rendering.
//
// This is a separate go.mod sub-module because g3n pulls CGO + OpenGL/GLFW
// drivers that require a GPU and display server.
// Import directly: github.com/golusoris/golusoris/media/3d
//
// # Usage
//
//	app, err := threed.NewApp("My Scene", 800, 600)
//	scene := threed.NewScene()
//	// add meshes, lights, cameras to scene
//	app.Run(scene)
package threed

import (
	"fmt"

	"github.com/g3n/engine/app"
	"github.com/g3n/engine/core"
	"github.com/g3n/engine/renderer"
)

// App wraps a g3n application window.
type App struct {
	a *app.Application
	r *renderer.Renderer
}

// NewApp creates a new 3D application window.
func NewApp(title string, width, height int) (*App, error) {
	a, err := app.Create(width, height, title)
	if err != nil {
		return nil, fmt.Errorf("3d: create app: %w", err)
	}
	r := renderer.NewRenderer(a.Gls())
	if err := r.AddDefaultShaders(); err != nil {
		return nil, fmt.Errorf("3d: add shaders: %w", err)
	}
	return &App{a: a, r: r}, nil
}

// Run starts the render loop with scene as the root node.
func (a *App) Run(scene *core.Node) {
	a.a.Run(func(rend *renderer.Renderer, _ interface{}) {
		_ = rend.Render(scene, nil)
	})
}

// Scene wraps a g3n core.Node as the scene root.
type Scene = core.Node

// NewScene creates a new scene root node.
func NewScene() *core.Node {
	return core.NewNode()
}
