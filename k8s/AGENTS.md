<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/

Kubernetes-aware modules. All opt-in.

| Subpackage | Purpose |
|---|---|
| `k8s/podinfo` | Downward-API env → typed `PodInfo` via fx (k8s-only view) |
| `k8s/health` | `/livez` `/readyz` `/startupz` backed by `statuspage.Registry` |
| `k8s/metrics/prom` | Prometheus `/metrics` endpoint |
| `k8s/client` | client-go wrapper, in-cluster + kubeconfig + workload identity |
| `k8s/operator` | controller-runtime manager lifecycle + caller-supplied schemes |
| `k8s/nri` | split-module containerd NRI plugin registration + bounded hooks |

Leader election lives under top-level `leader/` so non-k8s apps can
elect via pg advisory lock. `leader/k8s` is k8s-Lease backend;
`leader/pg` is PostgreSQL backend.

Runtime-agnostic identity lives under top-level `container/runtime/` —
prefer it for new code. `k8s/podinfo` stays as k8s-only view for
code paths that are already k8s-specific (client, Lease users).

## Conventions

- Every module is opt-in. Apps not running on k8s skip them.
- Health checks live on **shared** `statuspage.Registry` — `/livez` `/readyz` `/startupz` `/status` all read from one source.
- Pod metadata: `core/log/` and `otel/` read env vars directly (lower in dep graph). Higher-level packages inject `podinfo.PodInfo` or `runtime.Info`.
