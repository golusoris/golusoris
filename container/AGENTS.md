# Agent guide — container/

Runtime-agnostic container + process concerns.

| Subpackage | Purpose |
|---|---|
| `container/runtime` | Detect runtime (k8s/docker/podman/systemd/bare) + unified `Info` |

Future additions (when needed):
- `container/oci/` — OCI image-spec helpers (manifest parsing, label reading)
- `container/resources/` — cgroup-based CPU/memory quota inspection

## Conventions

- These packages never import `k8s.io/*` or cloud SDKs. They work
  identically in every runtime. Platform-specific code lives under
  the platform's own directory (`k8s/`, future `docker/`, `systemd/`).
