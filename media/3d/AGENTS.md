# Agent guide — media/3d/

Thin wrapper over g3n/engine for 3D rendering. Package name is `threed` (the dir
is `3d`). Direct-import constructor — **no fx wiring**; this is a windowed render
loop, not a server component.

## API

```go
app, err := threed.NewApp()   // g3n singleton window + default shaders
scene := threed.NewScene()    // *core.Node root; add meshes/lights/cameras
app.Run(scene)                // blocks on the g3n render loop
```

`threed.Scene` is an alias for g3n `core.Node`. g3n v0.2 owns a single global
window via `app.App()` — title/size are g3n-managed, not constructor args.

## Why g3n/engine

- The most complete pure-Go 3D engine (scene graph, shaders, GLTF loaders) that
  binds OpenGL directly rather than wrapping a C engine.

## Notes

- **CGO-gated, own go.mod sub-module.** Pulls OpenGL/GLFW drivers — needs a GPU and
  a display server; will not run on a headless CI box. Import directly:
  `github.com/golusoris/golusoris/media/3d`.
- `App.Run` blocks the calling goroutine until the window closes — own the main
  goroutine; do not call it from inside an fx lifecycle hook.
