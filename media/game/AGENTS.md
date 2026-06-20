# Agent guide — media/game/

Thin wrapper over Ebitengine for 2D games and interactive apps. Direct-import —
**no fx wiring**; this is a windowed game loop, not a server component.

## API

```go
type MyGame struct{}
func (g *MyGame) Update() error                  { return nil }
func (g *MyGame) Draw(screen *game.Image)        { /* draw */ }
func (g *MyGame) Layout(ow, oh int) (int, int)   { return 320, 240 }

err := game.Run(&MyGame{}, game.Options{Title: "Hello", Width: 320, Height: 240})
img := game.NewImage(64, 64) // off-screen *ebiten.Image
```

`game.Game` and `game.Image` are aliases for `ebiten.Game` / `ebiten.Image`.
`Options` defaults: Width 640, Height 480, Title "golusoris game"; also
`WindowScale`, `Fullscreen`, `VSync`.

## Why Ebitengine

- The dominant pure-Go 2D game library — simple `Update`/`Draw`/`Layout` contract,
  cross-platform windowing/audio behind one façade.

## Notes

- **CGO-gated, own go.mod sub-module.** Pulls audio/video drivers and platform
  windowing libs; needs a display. Import directly:
  `github.com/golusoris/golusoris/media/game`.
- `game.Run` blocks until the window closes — own the main goroutine; do not call
  it from an fx lifecycle hook.
