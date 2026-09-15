<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/ginkgofx/

Wires a `go.uber.org/fx` application into a `github.com/onsi/ginkgo/v2` spec
suite's lifecycle: starts the app before specs run and stops it afterwards,
each bounded by an explicit `context.WithTimeout` (HISS-02), and gives specs
a way to resolve fx-provided values via `Populate`.

## API

```go
// Suite-scoped: one app for the whole spec suite. Populate targets are
// resolved as soon as fx.New(opts...) returns, before Start runs.
var svc *myservice.Service
var h = ginkgofx.Setup(myservice.Module, ginkgofx.Populate(&svc))

var _ = Describe("MyService", func() {
    It("does the thing", func() {
        Expect(svc.DoThing()).To(Succeed())
    })
})

// Spec-scoped: a fresh app per It. Call it inside the Describe/Context it
// should apply to — same scoping rule as calling ginkgo.BeforeEach
// directly, a package-level call would run before every spec in the suite.
var _ = Describe("MyService", func() {
    _ = ginkgofx.SetupEach(myservice.Module, ginkgofx.Populate(&svc))
    It("does the thing", func() { ... })
})

// Explicit timeouts (defaults: 15s each):
ginkgofx.SetupWithOptions(ginkgofx.Options{StartTimeout: 5 * time.Second}, opts...)

// Lower-level primitives Setup/SetupEach build on, for hand-rolled wiring:
err := ginkgofx.StartApp(ctx, app, timeout)
err  = ginkgofx.StopApp(ctx, app, timeout)
```

`Setup`/`SetupWithOptions` register Ginkgo's `BeforeSuite`/`AfterSuite`;
`SetupEach`/`SetupEachWithOptions` register `BeforeEach`/`AfterEach`. Ginkgo
allows only one `BeforeSuite` and one `AfterSuite` handler per suite, so call
`Setup`/`SetupWithOptions` at most once per suite (same restriction as
`ginkgo.BeforeSuite` itself). A failed start/stop calls `ginkgo.Fail`, which
panics to end the current spec — Ginkgo catches it, same as any other
assertion failure.

## Root module, not a nested go.mod

Unlike `testutil/pact` (own go.mod because `pact-go` embeds a ~40 MB Ruby
standalone binary), `ginkgo`/`gomega` are pure Go with no embedded binaries
and were already indirect dependencies of this module (pulled in via
`sigs.k8s.io/controller-runtime`'s own test requirements) before this
package existed — promoting them to direct requirements added no new build
weight. Every other heavy test-only dependency in `testutil/`
(`testcontainers-go`, `tsenart/vegeta`, `leanovate/gopter`, `go-mutesting`,
`gofakeit`) already lives directly in the root `go.mod` for the same reason.
Root placement also keeps this package inside `make verify-all`'s default
gate (`MODULES := . core`); nested/native sub-modules build only on demand
and are skipped by that default gate, which would be the wrong trade for a
package that carries its own tests.

## Don't

- Don't call `Setup`/`SetupWithOptions` more than once per suite — Ginkgo
  only allows one `BeforeSuite`/`AfterSuite` handler.
- Don't call `SetupEach` at package level when you mean to scope it to one
  `Describe` — register it inside that Describe's closure.
- Don't rely on `fx.StartTimeout`/`fx.StopTimeout` fx options for the bound:
  `fx.App.Start`/`Stop` only honor those through `fx.App.Run`, not a direct
  `Start(ctx)`/`Stop(ctx)` call, so `Options.StartTimeout`/`StopTimeout` (via
  `context.WithTimeout`) are what actually bound these calls.
