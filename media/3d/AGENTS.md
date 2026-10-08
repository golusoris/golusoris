<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — media/3d/

Thin wrapper over g3n/engine for 3D rendering. Package name is `threed` (dir
is `3d`). Direct-import constructor — **no fx wiring**; this is windowed render
loop, not server component.

## API

```go
app, err := threed.NewApp()   // g3n singleton window + default shaders
scene := threed.NewScene()    // *core.Node root; add meshes/lights/cameras
err = app.Run(scene)          // blocks on the g3n render loop; first render error
```

`threed.Scene` is alias for g3n `core.Node`. g3n v0.2 owns single global
window via `app.App()` — title/size are g3n-managed, not constructor args.

## Why g3n/engine

- most complete pure-Go 3D engine (scene graph, shaders, GLTF loaders) that
 binds OpenGL directly rather than wrapping C engine.

## Notes

- **Linux+CGO renderer, own go.mod sub-module.** Pulls OpenGL/GLFW drivers and
  needs GPU plus display server. Other builds keep scene types available;
  `NewApp` and `Run` return `ErrUnsupported`. Import directly:
  `github.com/golusoris/golusoris/media/3d`.
- `App.Run` blocks calling goroutine until window closes — own main
 goroutine; do not call it from inside fx lifecycle hook.
