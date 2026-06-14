---
name: scaffold-operator
description: Use when adding a Kubernetes operator (CRD + reconciler) to a golusoris app — walks CRD API types → controller-gen codegen → scheme registration → reconciler → fx-wire against operator.Module → envtest.
---

# scaffold-operator

Scaffold a Kubernetes CRD + reconciler on top of `golusoris/k8s/operator`
(controller-runtime). The framework's `operator.Module` already provides and
runs the `manager.Manager`; this skill adds the app-side CRD type, its
generated code, and a reconciler wired into fx.

## Prerequisites

- App composes `golusoris.K8sOperator` (i.e. `operator.Module`).
- `controller-gen` available: `go run sigs.k8s.io/controller-tools/cmd/controller-gen@latest`.

## Steps

### 1. Define the CRD API types

Create `api/<group>/<version>/<kind>_types.go`, e.g. `api/example/v1/widget_types.go`:

```go
package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type Widget struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`
    Spec              WidgetSpec   `json:"spec,omitempty"`
    Status            WidgetStatus `json:"status,omitempty"`
}

type WidgetSpec struct {
    // Replicas is the desired number of widget pods.
    Replicas int32 `json:"replicas"`
}

type WidgetStatus struct {
    Ready int32 `json:"ready"`
}

// +kubebuilder:object:root=true
type WidgetList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []Widget `json:"items"`
}
```

Add `groupversion_info.go` with the `SchemeBuilder` + `AddToScheme` for the
group/version (standard kubebuilder boilerplate), exporting `AddToScheme`.

### 2. Generate deepcopy + CRD manifests

```bash
controller-gen object paths=./api/...
controller-gen crd paths=./api/... output:crd:dir=config/crd/bases
```

`object` writes `zz_generated.deepcopy.go`; `crd` writes the CRD YAML to apply
to the cluster.

### 3. Register the scheme

Wire the generated `AddToScheme` into the manager via the framework helper:

```go
operator.ProvideScheme(examplev1.AddToScheme)
```

### 4. Write the reconciler

`internal/controller/widget_controller.go`:

```go
type WidgetReconciler struct{ client.Client }

func (r *WidgetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    var w examplev1.Widget
    if err := r.Get(ctx, req.NamespacedName, &w); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    // ... converge actual state toward w.Spec, then update w.Status ...
    return ctrl.Result{}, nil
}

func (r *WidgetReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).For(&examplev1.Widget{}).Complete(r)
}
```

### 5. Wire it into fx

```go
fx.New(
    golusoris.Core,
    golusoris.K8sOperator,
    operator.ProvideScheme(examplev1.AddToScheme),
    fx.Invoke(func(mgr manager.Manager) error {
        return (&WidgetReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr)
    }),
).Run()
```

### 6. Test with envtest

Reconciler tests use controller-runtime `envtest` (a local `kube-apiserver` +
`etcd`):

```bash
setup-envtest use --bin-dir bin -p path   # one-time: fetch the binaries
KUBEBUILDER_ASSETS=$(setup-envtest use --bin-dir bin -p path) go test ./internal/controller/...
```

Spin the env in `TestMain`, apply the CRD from `config/crd/bases`, then drive
the reconciler with a fake or real client.

## Gate

`gofumpt` + `gci` the generated + handwritten Go; `golangci-lint run`; reconciler
errors must wrap (`%w`). Generated files (`zz_generated.*`) are excluded from
gosec via `-exclude-generated`.
