# Agent guide — k8s/

Kubernetes-aware modules. All opt-in.

| Subpackage | Purpose |
|---|---|
| `k8s/podinfo` | Downward-API env → typed `PodInfo` via fx |
| `k8s/health` | `/livez` `/readyz` `/startupz` backed by `statuspage.Registry` |
| `k8s/metrics/prom` | Prometheus `/metrics` endpoint (Step 6b) |
| `k8s/leader` | k8s Lease leader election (Step 6b) |
| `k8s/client` | client-go wrapper, in-cluster + kubeconfig + workload identity (Step 6c) |

## Conventions

- Every module is opt-in. Apps not running on k8s skip them.
- Health checks live on the **shared** `statuspage.Registry` — `/livez` `/readyz` `/startupz` `/status` all read from one source.
- Pod metadata: `log/` and `otel/` read the env vars directly (lower in dep graph). Higher-level packages inject `podinfo.PodInfo`.
