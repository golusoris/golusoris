<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/nfd/

Node Feature Discovery local feature source writer. Node agent writes
labels into nfd-worker `features.d`; NFD turns them into node labels. No
Node RBAC needed. Format verified against NFD v0.19.0
(`docs/usage/customization-guide.md` "Local feature source",
`source/local/local.go`, `pkg/apis/nfd/validate/validate.go`).

## Key surface

| Symbol | Purpose |
| --- | --- |
| `WriteFeatureFile(dir, name, labels)` | atomic write, no expiry |
| `WriteFeatureFileUntil(dir, name, labels, expiry)` | atomic write, `# +expiry-time=<RFC3339 UTC>` first line |
| `ValidateLabel(key, value)` | NFD label rules; `ErrInvalidLabel` |
| `Static(labels)` | fixed `Source` |
| `NewWriter(opts, src, clk)` + `Writer.Refresh(ctx)` | non-fx refresh |
| `Module(src)` | fx: write on start, rewrite every `refresh` |

## Wiring

```go
fx.New(
    golusoris.Core,
    nfd.Module(func(ctx context.Context) (map[string]string, error) {
        return map[string]string{"vmafx.io/backend.cuda": "true"}, nil
    }),
)
```

DaemonSet mounts hostPath `/etc/kubernetes/node-feature-discovery/features.d`
(same mount nfd-worker uses).

## Config

Prefix `k8s.nfd` (env `APP_K8S_NFD_*`): `enabled` (false), `dir`
(`DefaultDir`), `name` (required), `ttl` (10m), `refresh` (2m, must be
below `ttl`), `timeout` (10s per Source call + write).

## Rules enforced

- File name: 1..200 bytes, single path element, no leading dot (NFD skips
 dot files; temp files use dot prefix so nfd-worker never reads them).
- Key: prefixed label key; `kubernetes.io` + `*.kubernetes.io` denied
 except `feature.node.kubernetes.io`, `profile.node.kubernetes.io` and
 subdomains. Unprefixed keys rejected (NFD auto-prefix deprecated).
- Value: Kubernetes label value (<=63, alnum edges, `-_.`).
- Rendered file > 64 KiB -> `ErrTooLarge`; NFD ignores larger files.
- Write = temp in same dir + fsync + chmod 0644 + rename. Failure leaves
 previous file untouched, temp removed.
- Windows rename: open reader handle (no `FILE_SHARE_DELETE`) -> `ERROR_ACCESS_DENIED`
 / `ERROR_SHARING_VIOLATION`. `rename.go` retries those only, 64 attempts,
 1ms..32ms backoff (~1.9s, `cmd/internal/robustio` 2s). Other OS: one attempt.

## Don't

- Don't remove file on stop: DaemonSet rollout would drop labels between
 pods. Expiry retires labels of dead agent after next NFD rediscovery.
- Don't set `ttl` near NFD `core.sleepInterval` (60s default): expiry only
 evaluated per rediscovery.
- Don't share one file name between two writers; last rename wins.
