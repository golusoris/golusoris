<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/nri/

Opt-in fx module wrapping [containerd/nri](https://github.com/containerd/nri)'s
plugin stub: register a plugin under a configurable name/index, implement the
pod/container lifecycle hooks the fleet needs as plain functions, and get every
call bounded by a context timeout automatically.

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

`Module` requires a `Hooks` value in the fx graph (via `ProvideHooks`) and
connects to the runtime's NRI socket (`/var/run/nri/nri.sock` by default) on
fx Start — it needs to run where that socket is mounted (typically a
DaemonSet on the node, or the containerd host itself).

## Why its own module

`github.com/containerd/nri`'s `pkg/api` unconditionally imports
`tetratelabs/wazero` (a full WASM runtime, for NRI's optional WASM-plugin
support) plus `containerd/ttrpc` and `opencontainers/runtime-spec` — none of
which the root module's `k8s.io/client-go` + `sigs.k8s.io/controller-runtime`
graph already carries, unlike `k8s/operator` and `k8s/client` (root-module:
their heavy deps are already there and shared by several `k8s/*` packages).
NRI has exactly two fleet consumers today and pulls in a WASM VM neither
needs — splitting it out keeps that weight off every other app, per the
framework's convention for heavy/native-dep packages (README.md "Specialty
sub-modules").

It is deliberately not `k8s/operator/nri` or a file in `k8s/operator/`:
`operator` wraps a controller-runtime `manager.Manager` against the
Kubernetes API server; this package talks to containerd directly over a
Unix socket and needs no Kubernetes API access at all. They compose (an app
can run both), but neither depends on the other.

## Testing

`nri_test.go` covers construction (`newRegistration` — hook validation,
timeout validation, name/index → stub-option wiring) against a hand-rolled
`fakeStub` implementing `nristub.Stub`, and the `*plugin` adapter's hook
dispatch (round-trip, error propagation, timeout) directly — no real
containerd socket is ever opened. `New` itself is also safe to unit-test
without a cluster: `nristub.New` only validates and assigns identity: the
real connection happens on `Stub.Start`/`Stub.Run`.

## Don't

- Don't call `Registration.Stub.Start`/`.Run` yourself when using `Module` —
  it owns the lifecycle.
- Don't leave a `Hooks` field nil hoping NRI simply won't subscribe to that
  event — `New`/`newRegistration` reject a `Hooks` value with any nil field.
  Add a genuine no-op function instead if a consumer truly doesn't act on one
  of the four.
- Don't remove the `bounded` timeout wrapper to "simplify" a hook — an NRI
  hook that hangs blocks the containerd request that triggered it.
