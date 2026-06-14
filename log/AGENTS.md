# Agent guide — log/

golusoris's logging layer. Provides a configured `*slog.Logger` with one of two
handlers and attaches k8s pod metadata as default attributes.

- **tint** (colored, human-readable) when stdout is a TTY or `LOG_FORMAT=tint`
- **JSON** (structured, production-friendly) otherwise

`Module` also calls `slog.SetDefault`, so code that hasn't migrated still gets
the configured handler.

## Key API

| Symbol | Purpose |
|---|---|
| `log.Module` | fx module — provides `*slog.Logger`, sets slog default |
| `log.New(Options)` | build a logger directly (tests, non-fx callers) |
| `log.Options` | `Format`, `Level`, `Output`, `AddSource` (zero value usable) |
| `log.Format` consts | `FormatAuto` / `FormatTint` / `FormatJSON` |
| `log.LevelFromString(s)` | parse a level name → `(slog.Level, ok)` |

## Config (env, read by `Module`)

```
LOG_LEVEL  = debug | info | warn | error   # default info
LOG_FORMAT = auto | tint | json            # default auto
```

Pod attrs are auto-attached when present (k8s downward API):
`POD_NAME`, `POD_NAMESPACE`, `POD_IP`, `NODE_NAME`, `SERVICE_ACCOUNT`.

## Usage

```go
fx.New(log.Module, fx.Invoke(func(l *slog.Logger) {
    l.InfoContext(ctx, "started", "addr", addr)
}))
```

## Don't

- Don't `fmt.Println` / `fmt.Printf` for diagnostics — inject `*slog.Logger`
  and use slog (the project lint gate rejects fmt prints).
- Don't log secrets, tokens, or full request bodies — even at debug level.
- Don't build ad-hoc loggers with stdlib `slog.New` in app code — use
  `log.New` / `log.Module` so format + pod attrs stay consistent.
- Don't use `Info`/`Error` without context — prefer the `*Context` variants so
  trace correlation works.
