// Package game provides a thin wrapper over Ebitengine for 2D games and
// interactive apps.
//
// This is a separate go.mod sub-module because Ebitengine pulls CGO + audio/
// video drivers and platform-specific windowing libraries.
// Import directly: github.com/golusoris/golusoris/media/game
//
// # Usage
//
//	type MyGame struct{}
//	func (g *MyGame) Update() error          { return nil }
//	func (g *MyGame) Draw(screen *ebiten.Image) { /* draw */ }
//	func (g *MyGame) Layout(ow, oh int) (int, int) { return 320, 240 }
//
//	game.Run(&MyGame{}, game.Options{Title: "Hello", Width: 320, Height: 240})
package game

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
)

// Game is the Ebitengine game interface (re-exported for convenience).
type Game = ebiten.Game

// Image is ebiten.Image (re-exported).
type Image = ebiten.Image

// Options configures the game window.
type Options struct {
	Title       string
	Width       int
	Height      int
	WindowScale float64
	Fullscreen  bool
	VSync       bool
}

// Run starts the game loop. Blocks until the window is closed.
func Run(g Game, opts Options) error {
	if opts.Width <= 0 {
		opts.Width = 640
	}
	if opts.Height <= 0 {
		opts.Height = 480
	}
	if opts.Title == "" {
		opts.Title = "golusoris game"
	}

	ebiten.SetWindowTitle(opts.Title)
	ebiten.SetWindowSize(opts.Width, opts.Height)
	ebiten.SetFullscreen(opts.Fullscreen)
	ebiten.SetVsyncEnabled(opts.VSync)
	if opts.WindowScale > 0 {
		ebiten.SetWindowSize(
			int(float64(opts.Width)*opts.WindowScale),
			int(float64(opts.Height)*opts.WindowScale),
		)
	}

	if err := ebiten.RunGame(g); err != nil {
		return fmt.Errorf("game: run: %w", err)
	}
	return nil
}

// NewImage creates a new off-screen image.
func NewImage(width, height int) *ebiten.Image {
	return ebiten.NewImage(width, height)
}
