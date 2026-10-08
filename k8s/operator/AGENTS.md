<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/operator/

Opt-in fx module wrapping [controller-runtime](https://sigs.k8s.io/controller-runtime)
`manager.Manager`, so apps ship Kubernetes CRDs + reconcilers way they ship
HTTP handlers. module builds manager, assembles its scheme from
app-supplied CRD types, and runs `mgr.Start` under fx lifecycle.

## Key surface

| Symbol | Purpose |
| --- | --- |
| `Module` | Provides `manager.Manager`, runs it on fx Start, stops on Stop |
| `Options` | `metrics_addr`, `health_probe_addr`, `leader_election[_id]`, `graceful_shutdown` (koanf, prefix `operator`) |
| `SchemeAdder` | `func(*runtime.Scheme) error` — a CRD's `AddToScheme` |
| `ProvideScheme(adder)` | `fx.Option` wiring a `SchemeAdder` into the manager's scheme group |

## Wiring

```go
fx.New(
    golusoris.Core,
    golusoris.K8sOperator,                 // operator.Module
    operator.ProvideScheme(myv1.AddToScheme),
    fx.Invoke(func(mgr manager.Manager) error {
        return (&MyReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr)
    }),
).Run()
```

`Module` resolves rest.Config via `ctrl.GetConfig` (in-cluster ServiceAccount or
kubeconfig), so it needs cluster access at start. default probes (`healthz` and
`readyz` ping) are wired automatically; point your Deployment's probes at
`health_probe_addr`.

## Testing

`operator_test.go` covers pure logic — scheme assembly, `Options →
manager.Options` mapping, defaults — without cluster. Reconciler-level
coverage belongs in **app** via controller-runtime `envtest` (downloads local `kube-apiserver` + `etcd`); that's integration concern, not shipped
here.

## Don't

- Don't call `mgr.Start` yourself — `Module` owns lifecycle; register
 reconcilers with `SetupWithManager` via fx.Invoke.
- Don't register CRD types by mutating global scheme in `init()` — use
 `ProvideScheme` so manager and its client share one scheme.
- Don't enable `leader_election` without setting `leader_election_id` (lease
 name); controller-runtime needs it.
- Don't assume manager is reachable in unit tests — it dials API server
 on Start. Use envtest for reconciler behaviour.
