- **BREAKING** (`core/id`): `Generator.NewUUID` no longer panics when the system random source fails; it returns the error. Before: `func (Generator) NewUUID() uuid.UUID` — after: `func (Generator) NewUUID() (uuid.UUID, error)`.

  ```go
  // before
  u := g.NewUUID()
  // after
  u, err := g.NewUUID()
  if err != nil { return err }
  ```

- **BREAKING** (`payments/subs`): `Options.IDGen` now returns an error, which `Service.Start` surfaces. Before: `IDGen func() string` — after: `IDGen func() (string, error)`.
- `core/config`: `Options.Logger *slog.Logger` added — receives file-watch / SIGHUP reload failures (nil = `slog.Default()`); a failed watch registration now fails the fx `OnStart` hook instead of being dropped.
- `core/mcp`: the stdio stdout-redirect `Close` now reports a drain failure; a refused `fx.Shutdowner.Shutdown` is logged.
- `core/codec/yaml`: `ReadFile` / `WriteFile` surface close and temp-file removal failures instead of discarding them.
