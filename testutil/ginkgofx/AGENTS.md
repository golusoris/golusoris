<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/ginkgofx/

Wires `go.uber.org/fx` application into `github.com/onsi/ginkgo/v2` spec
suite's lifecycle: starts app before specs run and stops it afterwards,
each bounded by explicit `context.WithTimeout` (HISS-02), and gives specs
way to resolve fx-provided values via `Populate`.

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

`Setup`/`SetupWithOptions` register Ginkgo's `BeforeSuite`/`AfterSuite`.
`SetupEach`/`SetupEachWithOptions` register `BeforeEach`/`AfterEach`. Ginkgo
allows one `BeforeSuite` and one `AfterSuite` handler per suite. Call
`Setup`/`SetupWithOptions` at most once per suite, matching
`ginkgo.BeforeSuite` restriction. Call only from suite's true top level:
package-level `var` or `init` func. Never nest it inside
`Describe`/`Context`/`When` closure. Ginkgo permits suite nodes only during
top-level tree construction. Container closures run later, after `RunSpecs`
starts walking tree. Ginkgo then rejects nested suite nodes, prints
"can only be called at the top level", and exits process.
`SetupEach`/`SetupEachWithOptions`
have no such restriction: `BeforeEach`/`AfterEach` are ordinary container
nodes, meant to be called from inside `Describe`/`Context` they scope
to (see below). `ginkgofx_wrongpattern_test.go` exercises `Setup`
failure mode end to end via re-exec'd subprocess (Ginkgo's `os.Exit(1)`
would otherwise tear down whole package's test run). failed
start/stop calls `ginkgo.Fail`, which panics to end current spec —
Ginkgo catches it, same as any other assertion failure.

## Root module, not a nested go.mod

Unlike `testutil/pact`, whose go.mod isolates `pact-go`'s ~40 MB Ruby binary,
`ginkgo` and `gomega` are pure Go with no embedded binaries. They were already indirect dependencies via
`sigs.k8s.io/controller-runtime` tests. Promoting them to direct requirements
added no build weight. Every other heavy test-only dependency in `testutil/`
(`testcontainers-go`, `tsenart/vegeta`, `leanovate/gopter`, `go-mutesting`,
`gofakeit`) already lives directly in root `go.mod` for same reason.
Root placement avoids separate module solely for pure-Go test helper.
`make verify-all` discovers and gates all 24 modules, including
`testutil/pact` and native submodules.

## Don't

- Don't call `Setup`/`SetupWithOptions` more than once per suite — Ginkgo
 only allows one `BeforeSuite`/`AfterSuite` handler.
- Don't call `Setup`/`SetupWithOptions` from inside `Describe`/`Context`/
 `When` closure — they register Ginkgo's `BeforeSuite`/`AfterSuite`, which
 Ginkgo only accepts at suite's true top level; nesting them makes
 Ginkgo exit process with "can only be called at the top level"
 instead of registering hook.
- Don't call `SetupEach` at package level when you mean to scope it to one
 `Describe` — register it inside that Describe's closure.
- Don't rely on `fx.StartTimeout`/`fx.StopTimeout` fx options for bound:
 `fx.App.Start`/`Stop` only honor those through `fx.App.Run`, not direct
 `Start(ctx)`/`Stop(ctx)` call, so `Options.StartTimeout`/`StopTimeout` (via
 `context.WithTimeout`) are what bound these calls.

## Naming: why this isn't `testutil/pact`

`docs/FLEET_GO_DEMAND.md`'s sprint list (cluster table and "First sprint"
list, item 3) names this sprint item `testutil/pact` as shorthand for "ginkgo fx-aware BDD lifecycle wrapper." That name was never meant literally:
`testutil/pact` already exists as unrelated package (Pact consumer-driven
contract testing, wrapping `pact-go`). This package landed as
`testutil/ginkgofx` instead, named for what it wraps
(`onsi/ginkgo`), to avoid colliding with that existing package.
