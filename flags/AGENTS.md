# Agent guide — flags/

Typed feature-flag evaluation with a pluggable [Provider]. Mirrors the
OpenFeature evaluation contract so migrating to the official OpenFeature Go SDK
is straightforward.

## Core types

| Type | Purpose |
|---|---|
| `Provider` | `Evaluate(ctx, key, default, evalCtx) (any, error)` + `Metadata()` |
| `EvalContext` | `map[string]any` — targeting attributes (userID, tenantID, …) |
| `Client` | `Bool / String / Int / Float` typed evaluators |
| `MemoryProvider` | In-memory provider for tests and local dev; `Set(key, val)`, `Delete(key)` |
| `NoopProvider` | Always returns default — safe null object for fx graphs without a provider |

## Usage

```go
p := flags.NewMemoryProvider()
p.Set("dark-mode", true)
p.Set("api-version", "v2")

client := flags.New(p)
if client.Bool(ctx, "dark-mode", false) { ... }
ver := client.String(ctx, "api-version", "v1")
```

## Provider contract

- Return `defaultValue, nil` for unknown flags (graceful degradation).
- Return `defaultValue, ErrUnknownFlag(key)` to signal strict mode to callers.
- `EvalContext` is optional; providers may ignore it.

## Don't

- Don't cache flag values across requests — evaluation should be cheap but fresh.
- Don't use `MemoryProvider` in production without a persistence backend.
- Don't gate on flags in DB migrations — flag evaluation requires the DB to be up.
