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
|---|---|
| `plugin.New[T](name)` | create a named `*Registry[T]` (T is usually an interface) |
| `Registry.Register(key, impl) error` | add impl; returns `ErrDuplicate` on a repeat key |
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

Resolve in an `fx.Invoke` with `Lookup` and return its error to fail fast on
misconfiguration.

## Don't

- Don't call `Register` twice for the same key — it returns `ErrDuplicate` by
  design (surface it at startup, like `http.Handle`). Use `MustRegister` only
  in tests.
- Don't use `Lookup` on the request hot path — resolve once at startup and
  hold the impl; the read lock is cheap but the error-on-miss is a startup
  contract, not a runtime one.
- Don't treat this as a security boundary — every registered impl runs in-process
  with full trust. It's a wiring mechanism, not a sandbox.
- Don't rely on `Keys()`/`All()` ordering — it's undefined.
