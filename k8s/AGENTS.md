# Agent guide — k8s/

Kubernetes-aware modules. All opt-in.

| Subpackage | Purpose |
|---|---|
| `k8s/podinfo` | Downward-API env → typed `PodInfo` via fx (k8s-only view) |
| `k8s/health` | `/livez` `/readyz` `/startupz` backed by `statuspage.Registry` |
| `k8s/metrics/prom` | Prometheus `/metrics` endpoint |
| `k8s/client` | client-go wrapper, in-cluster + kubeconfig + workload identity |

Leader election lives under top-level `leader/` so non-k8s apps can
elect via a pg advisory lock. `leader/k8s` is the k8s-Lease backend;
`leader/pg` is the PostgreSQL backend.

Runtime-agnostic identity lives under top-level `container/runtime/` —
prefer it for new code. `k8s/podinfo` stays as a k8s-only view for
code paths that are already k8s-specific (the client, Lease users).

## Conventions

- Every module is opt-in. Apps not running on k8s skip them.
- Health checks live on the **shared** `statuspage.Registry` — `/livez` `/readyz` `/startupz` `/status` all read from one source.
- Pod metadata: `log/` and `otel/` read the env vars directly (lower in dep graph). Higher-level packages inject `podinfo.PodInfo` or `runtime.Info`.
