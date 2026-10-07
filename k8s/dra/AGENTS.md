<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/dra/

DRA ResourceSlice publisher (`resource.k8s.io/v1`, GA k8s 1.34). Node
agent publishes its devices; scheduler allocates ResourceClaims by
attribute (backend, index, memory). Wraps upstream controller
`k8s.io/dynamic-resource-allocation/resourceslice` v0.37.1 (Apache-2.0):
diff desired pool vs API server, write only changes.

## Key surface

| Symbol | Purpose |
| --- | --- |
| `Device` | name + `Strings`/`Ints`/`Bools`/`Versions` attributes + `Capacity` (base units, BinarySI) |
| `NewPublisher(k, opts, logger)` | validate; unstarted |
| `Publisher.Start(ctx, devices)` | ctx bounds informer sync only |
| `Publisher.Update(devices)` | replace pool; identical devices -> no API call |
| `Publisher.Stop(ctx)` | stop controller + delete node's slices of driver |
| `Module(src)` | fx: provides `*Publisher`; `src` once on start |

## Wiring

```go
fx.New(
    golusoris.Core,
    client.Module,  // kubernetes.Interface
    podinfo.Module, // node name default
    dra.Module(probeGPUs), // func(ctx) ([]dra.Device, error)
)
```

Hotplug -> inject `*dra.Publisher`, call `Update`.

## Config

Prefix `k8s.dra` (env `APP_K8S_DRA_*`): `enabled` (false), `driver`
(DNS subdomain <= 63, required), `node` (default `PodInfo.NodeName`),
`pool` (default node).

## Rules enforced (client side, before API)

- Device name DNS label, unique in pool.
- Attribute/capacity name: C identifier <= 32, optional
 `<dns-subdomain <= 63>/` prefix; unique across all maps; <= 32 per device.
- String/version value <= 64 bytes; version = semver 2.0.0.
- > 128 devices -> split into slices of 128. Zero devices -> one empty
 slice (driver up, no devices).

## Decisions

- Publisher only. `kubeletplugin` (prepare devices for containers, gRPC
 to kubelet) pulls `k8s.io/kubelet`, etcd `client/pkg`, CDI specs; DRA
 driver wires it directly, using `kubeletplugin.Helper.PublishResources`
 instead of this package.
- Root module, no submodule: `resourceslice` imports only api,
 apimachinery, client-go, klog, utils (already in graph); go.mod gains
 one line.
- Slices owned by Node -> GC on node delete; RBAC: get nodes;
 get/list/watch/create/update/delete `resourceslices`.

## Don't

- Don't run two publishers for one driver on one node: each deletes
 other's slices.
- Don't expect `Stop` to keep slices: rollout drops devices until new pod
 publishes. Allocated claims stay valid.
