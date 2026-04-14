Add a new opt-in fx module to the golusoris framework.

## Task

Create the package at `$ARGUMENTS` (e.g. `notify/slack`).

Follow these rules exactly:

1. **Package doc comment** — one paragraph: what it does, no new dep if possible.
2. **Config struct** — koanf-tagged, koanf prefix matches package path with dots.
3. **fx.Module var** — named `Module`, registered as `"golusoris.<pkg>"`.
4. **params struct** — `fx.In` embedding. Use `*config.Config` (pointer) to avoid mutex-copy govet error.
5. **fx lifecycle** — OnStart for health check / connection ping, OnStop for cleanup.
6. **No `time.Now()`** — inject `clock.Clock` if time is needed.
7. **Logging** — accept `*slog.Logger` via fx.In; never `fmt.Println`.
8. **Errors** — wrap with `fmt.Errorf("pkg: action: %w", err)`.
9. **AGENTS.md** — write per-subpackage guide: fx wiring, config table, usage example, Don't section.
10. **Tests** — table-driven, race-clean, integration over mocks. Use `testutil/fxtest` for fx lifecycle tests.
11. **Lint** — `golangci-lint run ./path/...` must report 0 issues before declaring done.

## Template structure

```
<pkg>/
  <pkg>.go       # package + Config + Module + client/server type
  <pkg>_test.go  # unit + integration tests
  AGENTS.md      # per-package agent guide
```

After writing, run:
- `go build ./<pkg>/...`
- `go test -race -count=1 ./<pkg>/...`
- `golangci-lint run ./<pkg>/...`

Then update:
- `AGENTS.md` layout tree (top-level)
- `README.md` "Landed so far"
- `.workingdir/STATE.md` session log
