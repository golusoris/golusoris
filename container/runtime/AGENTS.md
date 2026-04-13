# Agent guide — container/runtime

Unified runtime detection + identity. Successor to `k8s/podinfo` for code
that needs to work on k8s AND Docker AND systemd AND bare Linux.

## Detection order

1. Kubernetes (SA token at `/var/run/secrets/kubernetes.io/serviceaccount/token`).
2. Podman (`/run/.containerenv` or cgroup contains `libpod`).
3. Docker (`/.dockerenv` or cgroup contains `docker`).
4. systemd (`NOTIFY_SOCKET` or `INVOCATION_ID` env set).
5. Bare (fallthrough).

Order matters: k8s is checked first because its containers also show
`docker` / `containerd` in cgroups. Podman before Docker because it has
distinct markers and apps on Podman usually expect RuntimePodman.

## Conventions

- `runtime.Info` is the single truth for identity. log, otel, and metrics
  should attribute records with `Info.Hostname` + `Info.ContainerID` +
  platform-specific fields when populated.
- k8s-specific fields (PodName, Namespace, …) are only populated when
  `Runtime == RuntimeK8s`. Don't branch on them directly — check Runtime.
- `k8s/podinfo` stays as a k8s-only view — use it when the code path is
  already k8s-only (leader election, client-go users). Everywhere else,
  prefer `runtime.Info`.

## Don't

- Don't add runtime-specific helpers here. Platform-specific logic belongs
  in the platform's own package (`k8s/`, future `docker/`, `systemd/`).
  This package is the detection + unified view only.
