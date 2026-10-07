<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# k8s.io/client-go — v0.37.0 snapshot

Pinned: **v0.37.0**
Source: [tagged source](https://github.com/kubernetes/client-go/tree/v0.37.0)

## In-cluster client

```go
import (
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
)

cfg, err := rest.InClusterConfig()
client, err := kubernetes.NewForConfig(cfg)
```

## Kubeconfig client

```go
import "k8s.io/client-go/tools/clientcmd"

cfg, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
client, err := kubernetes.NewForConfig(cfg)
```

## Common operations

```go
// List pods
pods, err := client.CoreV1().Pods("namespace").List(ctx, metav1.ListOptions{})

// Get configmap
cm, err := client.CoreV1().ConfigMaps("namespace").Get(ctx, "name", metav1.GetOptions{})

// Create lease (leader election)
lease, err := client.CoordinationV1().Leases("namespace").Create(ctx, lease, metav1.CreateOptions{})

// Watch a bounded number of events under the caller's deadline.
watcher, err := client.CoreV1().Pods("namespace").Watch(ctx, metav1.ListOptions{})
if err != nil {
    return fmt.Errorf("watch pods: %w", err)
}
defer watcher.Stop()

const maxWatchEvents = 1_000
for processed := 0; processed < maxWatchEvents; processed++ {
    select {
    case <-ctx.Done():
        return ctx.Err()
    case event, ok := <-watcher.ResultChan():
        if !ok {
            return errors.New("pod watch closed")
        }
        pod, ok := event.Object.(*corev1.Pod)
        if !ok {
            return fmt.Errorf("unexpected pod watch object %T", event.Object)
        }
        handlePod(pod)
    }
}
```

## Leader election

```go
import (
    "k8s.io/client-go/tools/leaderelection"
    "k8s.io/client-go/tools/leaderelection/resourcelock"
)

lock := &resourcelock.LeaseLock{
    LeaseMeta:  metav1.ObjectMeta{Name: "my-lock", Namespace: "default"},
    Client:     client.CoordinationV1(),
    LockConfig: resourcelock.ResourceLockConfig{Identity: podName},
}

leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
    Lock:          lock,
    LeaseDuration: 15 * time.Second,
    RenewDeadline: 10 * time.Second,
    RetryPeriod:   2 * time.Second,
    Callbacks: leaderelection.LeaderCallbacks{
        OnStartedLeading: func(ctx context.Context) { /* ... */ },
        OnStoppedLeading: func() { /* ... */ },
        OnNewLeader:      func(identity string) { /* ... */ },
    },
})
```

## golusoris usage

- `k8s/client/` — `*kubernetes.Clientset` provided via Fx; in-cluster plus
  kubeconfig auto-detection.
- `leader/k8s/` — Kubernetes Lease-based leader election implementing the
  `leader.Elector` interface.

## Links

- [Changelog](https://github.com/kubernetes/client-go/blob/v0.37.0/CHANGELOG.md)
