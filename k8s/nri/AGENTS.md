<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/nri/

Opt-in fx module wrapping [containerd/nri](https://github.com/containerd/nri)'s
plugin stub: register plugin under configurable name/index, implement pod/container lifecycle hooks fleet needs as plain functions, and get every
call bounded by context timeout automatically.

**Own `go.mod` sub-module** (`github.com/golusoris/golusoris/k8s/nri`) — see
"Why its own module" below.

## Key surface

| Symbol | Purpose |
|---|---|
| `Hooks` | `CreateContainer` / `StartContainer` / `StopContainer` / `RemovePodSandbox` callbacks — all four mandatory |
| `Options` | `name`, `index`, `hook_timeout` (koanf, prefix `nri`) |
| `ProvideHooks(hooks)` | `fx.Option` supplying `Hooks` to `Module` |
| `New(opts, hooks)` | non-fx constructor — builds a `Registration` without connecting |
| `Registration.Stub` | the underlying `nristub.Stub` — `Start`/`Run`/`Stop`/`Wait` |

## Wiring

```go
fx.New(
    golusoris.Core,
    nri.Module,
    nri.ProvideHooks(nri.Hooks{
        CreateContainer:  myCreateContainer,
        StartContainer:   myStartContainer,
        StopContainer:    myStopContainer,
        RemovePodSandbox: myRemovePodSandbox,
    }),
).Run()
```

`Module` requires `Hooks` in fx graph via `ProvideHooks`. On fx Start, it
connects to runtime's NRI socket (`/var/run/nri/nri.sock` by default). Run it
where that socket is mounted, typically node DaemonSet or containerd host.

## Why its own module

`github.com/containerd/nri`'s `pkg/api` unconditionally imports
`tetratelabs/wazero` (full WASM runtime, for NRI's optional WASM-plugin
support) plus `containerd/ttrpc` and `opencontainers/runtime-spec` — none of
which root module's `k8s.io/client-go` + `sigs.k8s.io/controller-runtime`
graph already carries, unlike `k8s/operator` and `k8s/client` (root-module:
their heavy deps are already there and shared by several `k8s/*` packages).
NRI pulls in a WASM VM that other Kubernetes packages do not need. Separate
module keeps that weight off every other app, following README.md "Specialty
sub-modules" convention.

It is deliberately not `k8s/operator/nri` or file in `k8s/operator/`:
`operator` wraps controller-runtime `manager.Manager` against Kubernetes API server; this package talks to containerd directly over Unix socket and needs no Kubernetes API access at all. They compose (app
can run both), but neither depends on other.

## Testing

`nri_test.go` covers construction (`newRegistration` — hook validation,
timeout validation, name/index → stub-option wiring) against hand-rolled
`fakeStub` implementing `nristub.Stub`, and `*plugin` adapter's hook
dispatch (round-trip, error propagation, timeout) directly — no real
containerd socket is ever opened. `New` itself is also safe to unit-test
without cluster: `nristub.New` only validates and assigns identity: real connection happens on `Stub.Start`/`Stub.Run`.

## Don't

- Don't call `Registration.Stub.Start`/`.Run` yourself when using `Module` —
 it owns lifecycle.
- Don't leave a `Hooks` field nil hoping NRI won't subscribe to that
 event — `New`/`newRegistration` reject `Hooks` value with any nil field.
 Add genuine no-op function instead if consumer truly doesn't act on one
 of four.
- Don't remove `bounded` timeout wrapper to "simplify" hook — NRI
 hook that hangs blocks containerd request that triggered it.
