# deploy/logging

Loki + Promtail manifests for shipping golusoris-app logs to a Grafana Loki stack.

## What's here

| File | Purpose |
|---|---|
| `loki.yaml` | Single-instance Loki StatefulSet (dev / small prod). For scale-out use the [grafana/loki](https://grafana.com/docs/loki/latest/installation/helm/) Helm chart instead. |
| `promtail-daemonset.yaml` | Promtail DaemonSet that tails `/var/log/containers/*` on every node and ships to Loki. |
| `promtail-configmap.yaml` | Promtail scrape config — parses k8s labels, discovers pods, tags by namespace/app. |

## Quick start

```bash
# 1. Install Loki (single-instance, demo-grade)
kubectl apply -f deploy/logging/loki.yaml

# 2. Install Promtail
kubectl apply -f deploy/logging/promtail-configmap.yaml
kubectl apply -f deploy/logging/promtail-daemonset.yaml

# 3. Point Grafana at http://loki.logging:3100 as a data source.
```

## Production

For anything beyond a demo, use the official Helm charts:

```bash
helm repo add grafana https://grafana.github.io/helm-charts
helm upgrade --install loki grafana/loki-stack \
  --namespace logging --create-namespace \
  --set grafana.enabled=false \
  --set prometheus.enabled=false
```

The manifests here are reference scaffolding; real deployments should use the Helm chart for lifecycle management.

## golusoris-app log labels

Promtail's scrape config extracts the following labels from Kubernetes metadata:

- `namespace` — pod namespace
- `app` — `app.kubernetes.io/name` label
- `pod` — pod name
- `container` — container name
- `stream` — `stdout` / `stderr`

golusoris's `log/` package emits JSON (when `LOG_FORMAT=json`) — Promtail's `pipeline_stages` preserve the original JSON as the log line, so LogQL queries like `{app="myapp"} | json` work without extra parsing.
