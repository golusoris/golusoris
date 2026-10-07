<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# deploy/logging

Loki and Grafana Alloy manifests ship golusoris-app logs to a Loki stack.

## What's here

| File | Purpose |
| :--- | :--- |
| `loki.yaml` | Development-only, single-instance Loki. |
| `alloy-daemonset.yaml` | Node-local pod-log collector. |
| `alloy-configmap.yaml` | Discovery, parsing, labels, and delivery. |

## Quick start

```bash
# 1. Install Loki (single-instance, demo-grade)
kubectl apply -f deploy/logging/loki.yaml

# 2. Install Alloy
kubectl apply -f deploy/logging/alloy-configmap.yaml
kubectl apply -f deploy/logging/alloy-daemonset.yaml

# 3. Wait for Alloy before removing a previous Promtail installation.
kubectl rollout status daemonset/alloy -n logging
kubectl delete daemonset promtail -n logging --ignore-not-found
kubectl delete configmap promtail-config -n logging --ignore-not-found
kubectl delete clusterrole promtail --ignore-not-found
kubectl delete clusterrolebinding promtail --ignore-not-found
kubectl delete serviceaccount promtail -n logging --ignore-not-found

# 4. Point Grafana at http://loki.logging:3100 as a data source.
```

Alloy imports each node's old `/run/promtail/positions.yaml` once. When that
file is absent or corrupt, it starts at the end instead of replaying retained
logs. A GitOps installation with pruning enabled removes the deleted Promtail
objects automatically after the Alloy rollout is healthy.

## Production

The checked-in Loki manifest has no authentication and uses one
filesystem-backed replica. For production, use the maintained
[community Loki chart] and the official Alloy chart:

```bash
helm repo add grafana-community \
  https://grafana-community.github.io/helm-charts
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update
helm upgrade --install loki grafana-community/loki \
  --namespace logging --create-namespace --values loki-values.yaml
helm upgrade --install alloy grafana/alloy \
  --namespace logging --values alloy-values.yaml
```

The manifests here are reference scaffolding. Use the Helm charts for lifecycle
management in real deployments.

## golusoris-app log labels

Alloy extracts the following labels from Kubernetes metadata:

- `namespace` — pod namespace
- `app` — `app.kubernetes.io/name` label
- `pod` — pod name
- `container` — container name
- `stream` — `stdout` / `stderr`

golusoris's `core/log/` package emits JSON when `LOG_FORMAT=json`. Alloy's CRI stage
preserves the application payload, so LogQL queries such as
`{app="myapp"} | json` work without another parsing stage.

[community Loki chart]: https://grafana.com/docs/loki/latest/setup/install/helm/
