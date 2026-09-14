<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

- **BREAKING**: `markdown.RenderString` returns an error instead of panicking on
  a goldmark failure (HISS-07 burn-down).

  ```go
  // before
  html := markdown.RenderString(src) // panicked on error
  // after
  html, err := markdown.RenderString(src)
  ```

- **BREAKING**: `media/3d` `(*App).Run` returns the first frame render error
  instead of discarding it; the render loop still runs until the window closes.

  ```go
  // before
  app.Run(scene)
  // after
  err := app.Run(scene)
  ```

- **docs/epub**: `(*Book).WriteToWriter` now reports a temp-file close or
  remove failure (previously silently discarded); signature unchanged.
- **docs/xlsx**, **pdf/parse**, **media/img/pipeline**: `ReadRows`, `InfoFile`
  and the image handler surface close/write failures instead of discarding
  them; signatures unchanged.
