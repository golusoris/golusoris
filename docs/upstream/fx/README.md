<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# go.uber.org/fx — v1.24.0 snapshot

Pinned: **v1.24.0**
Source: [tagged source](https://github.com/uber-go/fx/tree/v1.24.0)
Godoc: [package documentation](https://pkg.go.dev/go.uber.org/fx@v1.24.0#section-documentation)

## Key API surface (v1.24.0)

### Application lifecycle

```go
app := fx.New(opts...)
app.Run()          // blocks until signal
app.Start(ctx)     // manual start
app.Stop(ctx)      // manual stop
```

### Core option constructors

```go
fx.Provide(constructor ...interface{})   // register providers
fx.Invoke(funcs ...interface{})          // eagerly call after DI
fx.Decorate(decorator ...interface{})    // override a type in a scope
fx.Supply(values ...interface{})         // provide concrete values directly
fx.Module(name string, opts ...Option)   // named sub-scope
fx.Options(opts ...Option)               // group options
fx.WithLogger(func() fxevent.Logger { return logger }) // override Fx event logger
fx.NopLogger                                        // silence Fx event logs
```

### Lifecycle hooks

```go
type Hook struct {
    OnStart func(context.Context) error
    OnStop  func(context.Context) error
}
lc.Append(fx.Hook{OnStart: ..., OnStop: ...})
```

### Annotation helpers

```go
fx.Annotate(f, fx.As(new(Interface)))          // bind to interface
fx.Annotate(f, fx.ResultTags(`name:"foo"`))    // named result
fx.Annotate(f, fx.ParamTags(`name:"foo"`))     // named param
fx.Annotate(f, fx.From(new(Type)))             // explicit input type
fx.Annotate(f, fx.ResultTags(`group:"key"`))   // value group
```

### Value groups

```go
// Provider — contribute to group
fx.Provide(fx.Annotate(newHandler, fx.ResultTags(`group:"handlers"`)))

// Consumer — receive slice of group
type Params struct {
    fx.In
    Handlers []Handler `group:"handlers"`
}
```

### `fx.In` / `fx.Out` structs

```go
type Params struct {
    fx.In
    DB  *pgxpool.Pool
    Cfg *config.Config `optional:"true"`
}

type Result struct {
    fx.Out
    Server *http.Server
    Mux    chi.Router   `name:"admin"`
}
```

## Patterns used in golusoris

Every subpackage exposes its capability via one of:

- `fx.Module("name", fx.Provide(...), fx.Invoke(...))` — preferred
- `fx.Options(fx.Provide(...))` — when no module name is needed

Never import internals directly; only compose via `fx.Module` or `fx.Options`.

## Breaking changes between v1.23 → v1.24

- `fx.WithLogger` takes a constructor that returns `fxevent.Logger`.
- `fx.Decorate` added in v1.18 — safe to use.

## Links

- [Changelog](https://github.com/uber-go/fx/blob/v1.24.0/CHANGELOG.md)
- [Package documentation](https://pkg.go.dev/go.uber.org/fx@v1.24.0)
