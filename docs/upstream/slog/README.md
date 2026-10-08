<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# log/slog — go1.27.1 stdlib snapshot

Pinned: **go1.27.1**
Source: [Go 1.27.1 source](https://cs.opensource.google/go/go/+/refs/tags/go1.27.1:src/log/slog/)

## Core usage

```go
import "log/slog"

// Default logger (writes to stderr)
slog.Info("message", "key", "value")
slog.Error("failed", "err", err)
slog.Warn("retrying", "attempt", 3)
slog.Debug("detail", "id", id)

// Context-aware: makes ctx available to the installed Handler.
slog.InfoContext(ctx, "message", "key", "value")
slog.ErrorContext(ctx, "failed", "err", err)
```

## Structured attrs

```go
slog.Info("request",
    slog.String("method", "GET"),
    slog.Int("status", 200),
    slog.Duration("latency", d),
    slog.Any("user", user),
)

// Group
slog.Info("db", slog.Group("query",
    slog.String("sql", q),
    slog.Duration("took", d),
))
```

## Custom logger

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level:     slog.LevelInfo,
    AddSource: true,
    ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
        // Rename "msg" to "message"
        if a.Key == slog.MessageKey {
            a.Key = "message"
        }
        return a
    },
}))
slog.SetDefault(logger)
```

## Child logger (with attrs)

```go
// WithGroup — namespace attrs
log := logger.WithGroup("request")
log.Info("handled", "method", "GET", "path", "/users")
// → {"request":{"method":"GET","path":"/users"}, "msg":"handled"}

// With — persistent attrs on every message
log := logger.With("service", "auth", "env", "prod")
log.Info("started")
// → {"service":"auth","env":"prod","msg":"started"}
```

## Level management

```go
lvl := &slog.LevelVar{}   // dynamic, safe for concurrent use
lvl.Set(slog.LevelDebug)

h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
```

## Custom handler interface

```go
type Handler interface {
    Enabled(ctx context.Context, level Level) bool
    Handle(ctx context.Context, r Record) error
    WithAttrs(attrs []Attr) Handler
    WithGroup(name string) Handler
}
```

## golusoris usage

- `core/log/` — slog factory: tint handler in development, JSON in production.
- `fmt.Println` and `log.Printf` are banned; always use slog via `core/log/`.
- Pass `ctx` to `slog.*Context` so handlers can inspect it. Trace export occurs
  only when the application installs `otel.ModuleWithSlogBridge`; slog does not
  propagate trace IDs automatically.

## Links

- [Standard-library documentation](https://pkg.go.dev/log/slog)
- [Structured logging design](https://go.googlesource.com/proposal/+/master/design/56345-structured-logging.md)
