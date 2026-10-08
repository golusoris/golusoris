<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — plugin/

Type-safe, in-process extension-point registry. Lets framework modules define
named extension points and lets apps (or other modules) register
implementations without import cycles. Pure-Go alternative to `.so` plugins —
no CGO, no subprocess, works on every GOOS.

## Key API

| Symbol | Purpose |
| --- | --- |
| `plugin.New[T](name)` | create named `*Registry[T]` (T is usually an interface) |
| `Registry.Register(key, impl) error` | add impl; returns `ErrDuplicate` on repeat key |
| `Registry.MustRegister(key, impl)` | add/replace (use in tests) |
| `Registry.Get(key)` | `(impl, ok)` |
| `Registry.Lookup(key) (T, error)` | impl or `ErrNotRegistered` (fail-fast at startup) |
| `Registry.Keys()` / `All()` / `Entries()` / `Len()` | snapshots |

## Usage

```go
// In the defining module:
var PaymentProviders = plugin.New[PaymentProvider]("payment.providers")

// In an app/feature module (fx.Invoke, so the error fails startup):
func wireStripe() error { return PaymentProviders.Register("stripe", &StripeProvider{}) }

// At runtime:
p, ok := PaymentProviders.Get("stripe")
```

Resolve in `fx.Invoke` with `Lookup` and return its error to fail fast on
misconfiguration.

## Don't

- Don't call `Register` twice for same key — it returns `ErrDuplicate` by
 design (surface it at startup, like `http.Handle`). Use `MustRegister` only
 in tests.
- Don't use `Lookup` on request hot path — resolve once at startup and
 hold impl; read lock is cheap but error-on-miss is startup
 contract, not runtime one.
- Don't treat this as security boundary — every registered impl runs in-process
 with full trust. It's wiring mechanism, not sandbox.
- Don't rely on `Keys()`/`All()` ordering — it's undefined.
