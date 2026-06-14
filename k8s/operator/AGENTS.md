# Agent guide — k8s/operator/

Opt-in fx module wrapping a [controller-runtime](https://sigs.k8s.io/controller-runtime)
`manager.Manager`, so apps ship Kubernetes CRDs + reconcilers the way they ship
HTTP handlers. The module builds the manager, assembles its scheme from
app-supplied CRD types, and runs `mgr.Start` under the fx lifecycle.

## Key surface

| Symbol | Purpose |
|---|---|
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
a kubeconfig), so it needs cluster access at start. The default probes (`healthz`
+ `readyz` ping) are wired automatically; point your Deployment's probes at
`health_probe_addr`.

## Testing

`operator_test.go` covers the pure logic — scheme assembly, `Options →
manager.Options` mapping, defaults — without a cluster. Reconciler-level
coverage belongs in the **app** via controller-runtime `envtest` (downloads a
local `kube-apiserver` + `etcd`); that's an integration concern, not shipped
here.

## Don't

- Don't call `mgr.Start` yourself — `Module` owns the lifecycle; just register
  reconcilers with `SetupWithManager` via fx.Invoke.
- Don't register CRD types by mutating a global scheme in `init()` — use
  `ProvideScheme` so the manager and its client share one scheme.
- Don't enable `leader_election` without setting `leader_election_id` (the lease
  name); controller-runtime needs it.
- Don't assume the manager is reachable in unit tests — it dials the API server
  on Start. Use envtest for reconciler behaviour.
